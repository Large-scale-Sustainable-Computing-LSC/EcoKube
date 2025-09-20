package main

import (
  "context"
  "encoding/json"
  "fmt"
  "io/ioutil"
  "log"
  "net/http"
  "os"
  "sort"
  "time"

  batchv1 "k8s.io/api/batch/v1"
  corev1 "k8s.io/api/core/v1"
  metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
  "k8s.io/apimachinery/pkg/fields"
  "k8s.io/apimachinery/pkg/util/intstr"
  "k8s.io/client-go/informers"
  "k8s.io/client-go/kubernetes"
  "k8s.io/client-go/rest"
)

type Site struct {
  PUE    float64 `json:"pue"`
  K      float64 `json:"k"`
  Region string  `json:"region"`
  CI     float64 `json:"ci"` // grams/kWh (static for MVP)
}
type Sites map[string]Site

func loadSites(path string) (Sites, error) {
  b, err := ioutil.ReadFile(path)
  if err != nil { return nil, err }
  var raw struct{ A Sites }
  // supports plain object {A:{...},B:{...}} as well
  var s Sites
  if err := json.Unmarshal(b, &s); err == nil { return s, nil }
  if err := json.Unmarshal(b, &raw); err == nil { return raw.A, nil }
  return nil, fmt.Errorf("invalid sites.json")
}

func chooseSite(sites Sites) string {
  type pair struct{ id string; score float64 }
  arr := []pair{}
  for id, v := range sites {
    score := v.CI * v.PUE * v.K // MVP: static signal
    arr = append(arr, pair{id, score})
  }
  sort.Slice(arr, func(i, j int) bool { return arr[i].score < arr[j].score })
  if len(arr) == 0 { return "" }
  return arr[0].id
}

func ensureAffinity(job *batchv1.Job, site string) {
  req := corev1.NodeSelectorRequirement{
    Key: "site", Operator: corev1.NodeSelectorOpIn, Values: []string{site},
  }
  term := corev1.NodeSelectorTerm{MatchExpressions: []corev1.NodeSelectorRequirement{req}}
  na := &corev1.NodeAffinity{
    RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
      NodeSelectorTerms: []corev1.NodeSelectorTerm{term},
    },
  }
  if job.Spec.Template.Spec.Affinity == nil {
    job.Spec.Template.Spec.Affinity = &corev1.Affinity{}
  }
  job.Spec.Template.Spec.Affinity.NodeAffinity = na
  if job.Annotations == nil { job.Annotations = map[string]string{} }
  job.Annotations["ci-aware/affinity-applied"] = "true"
  job.Labels = merge(job.Labels, map[string]string{"ciw/scheduler":"ci-aware","ciw/site":site})
}
func merge(a, b map[string]string) map[string]string {
  if a == nil { a = map[string]string{} }; for k,v := range b { a[k]=v }; return a
}

func main() {
  sitesPath := env("SITES_PATH", "/etc/ci-aware/sites.json")
  watchNS   := env("WORKLOAD_NS", "workloads")
  // PROM_URL reserved for later; static CI for MVP
  _, _ = http.Get // silence unused if removed during MVP

  sites, err := loadSites(sitesPath)
  if err != nil { log.Fatalf("load sites: %v", err) }
  selected := chooseSite(sites)
  if selected == "" { log.Fatalf("no sites found") }
  log.Printf("MVP static selection prefers site=%s", selected)

  cfg, err := rest.InClusterConfig()
  if err != nil { log.Fatalf("k8s cfg: %v", err) }
  cs, err := kubernetes.NewForConfig(cfg)
  if err != nil { log.Fatalf("k8s client: %v", err) }

  // Watch Jobs in target namespace
  factory := informers.NewSharedInformerFactoryWithOptions(cs, 0,
    informers.WithNamespace(watchNS),
    informers.WithTweakListOptions(func(lo *metav1.ListOptions) {
      lo.FieldSelector = fields.Everything().String()
    }))
  inf := factory.Batch().V1().Jobs().Informer()
  inf.AddEventHandler(&handler{cs: cs, ns: watchNS, site: selected})
  stop := make(chan struct{})
  factory.Start(stop)
  for t, ok := range factory.WaitForCacheSync(stop) {
    if !ok { log.Fatalf("cache sync failed for %v", t) }
  }
  log.Printf("ci-aware controller running; watching ns=%s", watchNS)
  <-stop
}

type handler struct{
  cs *kubernetes.Clientset
  ns string
  site string
}
func (h *handler) OnAdd(obj interface{}) {
  job := obj.(*batchv1.Job)
  if job.Annotations["ci-aware/affinity-applied"] == "true" { return }
  // mutate & update
  ensureAffinity(job, h.site)
  ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second); defer cancel()
  _, err := h.cs.BatchV1().Jobs(h.ns).Update(ctx, job, metav1.UpdateOptions{})
  if err != nil { log.Printf("update job %s: %v", job.Name, err) } else { log.Printf("affinity injected into %s", job.Name) }
}
func (*handler) OnUpdate(old, cur interface{}) {}
func (*handler) OnDelete(obj interface{}) {}

func env(k, d string) string { v:=os.Getenv(k); if v==""{return d}; return v }
