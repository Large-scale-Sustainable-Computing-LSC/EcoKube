package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sort"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
)

type Site struct {
	PUE    float64 `json:"pue"`
	K      float64 `json:"k"`
	Region string  `json:"region"`
	CI     float64 `json:"ci"` // grams/kWh (static for MVP)
}
type Sites map[string]Site

func loadSites(path string) (Sites, error) {
	b, err := os.ReadFile(path) // use os.ReadFile (no ioutil)
	if err != nil {
		return nil, err
	}
	var s Sites
	if err := json.Unmarshal(b, &s); err == nil {
		return s, nil
	}
	return nil, fmt.Errorf("invalid sites.json (expected object keyed by site id)")
}

func chooseSite(sites Sites) string {
	type pair struct {
		id    string
		score float64
	}
	arr := make([]pair, 0, len(sites))
	for id, v := range sites {
		score := v.CI * v.PUE * v.K // MVP: static signal
		arr = append(arr, pair{id: id, score: score})
	}
	if len(arr) == 0 {
		return ""
	}
	sort.Slice(arr, func(i, j int) bool { return arr[i].score < arr[j].score })
	return arr[0].id
}

func ensureAffinity(job *batchv1.Job, site string) {
	req := corev1.NodeSelectorRequirement{
		Key:      "site",
		Operator: corev1.NodeSelectorOpIn,
		Values:   []string{site},
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

	if job.Annotations == nil {
		job.Annotations = map[string]string{}
	}
	job.Annotations["ci-aware/affinity-applied"] = "true"

	if job.Labels == nil {
		job.Labels = map[string]string{}
	}
	job.Labels["ciw/scheduler"] = "ci-aware"
	job.Labels["ciw/site"] = site
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func main() {
	sitesPath := env("SITES_PATH", "/etc/ci-aware/sites.json")
	watchNS := env("WORKLOAD_NS", "workloads")

	sites, err := loadSites(sitesPath)
	if err != nil {
		log.Fatalf("load sites: %v", err)
	}
	selected := chooseSite(sites)
	if selected == "" {
		log.Fatalf("no sites found in %s", sitesPath)
	}
	log.Printf("MVP static selection prefers site=%s", selected)

	cfg, err := rest.InClusterConfig()
	if err != nil {
		log.Fatalf("k8s cfg: %v", err)
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		log.Fatalf("k8s client: %v", err)
	}

	// Watch Jobs in target namespace
	factory := informers.NewSharedInformerFactoryWithOptions(
		cs,
		0, // resync disabled
		informers.WithNamespace(watchNS),
		informers.WithTweakListOptions(func(lo *metav1.ListOptions) {
			lo.LabelSelector = "ciw/eligible=true"
		}),
	)

	inf := factory.Batch().V1().Jobs().Informer()

	inf.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			job, ok := obj.(*batchv1.Job)
			if !ok {
				return
			}
			// skip if already mutated
			if job.Annotations != nil && job.Annotations["ci-aware/affinity-applied"] == "true" {
				return
			}
			// mutate a deep copy and Update
			mut := job.DeepCopy()
			ensureAffinity(mut, selected)

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, err := cs.BatchV1().Jobs(watchNS).Update(ctx, mut, metav1.UpdateOptions{})
			if err != nil {
				log.Printf("update job %s: %v", job.Name, err)
			} else {
				log.Printf("affinity injected into %s (site=%s)", job.Name, selected)
			}
		},
		// Not needed for MVP, but methods provided to satisfy interface variants
		UpdateFunc: func(oldObj, newObj interface{}) {},
		DeleteFunc: func(obj interface{}) {},
	})

	stop := make(chan struct{})
	factory.Start(stop)

	// Wait for caches to sync
	if ok := cache.WaitForCacheSync(stop, inf.HasSynced); !ok {
		log.Fatalf("cache sync failed")
	}

	log.Printf("ci-aware controller running; watching ns=%s", watchNS)
	<-stop
}
