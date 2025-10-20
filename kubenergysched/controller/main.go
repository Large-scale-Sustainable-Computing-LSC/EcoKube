package main

import (
	"bufio"
	"context"
	stdjson "encoding/json"
	"errors"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/g-uva/KubEnergySched/kubenergysched/pkg/engine"
	"github.com/g-uva/KubEnergySched/kubenergysched/pkg/providers"
	"github.com/g-uva/KubEnergySched/kubenergysched/pkg/types"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	apitypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
)

type jsonlWriter struct {
	mu sync.Mutex
	w  *bufio.Writer
	f  *os.File
}

func newJSONLWriter(path string) (*jsonlWriter, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	return &jsonlWriter{w: bufio.NewWriter(f), f: f}, nil
}

func (tw *jsonlWriter) WriteOne(v any) error {
	if tw == nil || tw.w == nil {
		return nil
	}
	tw.mu.Lock()
	defer tw.mu.Unlock()
	b, err := stdjson.Marshal(v)
	if err != nil {
		return err
	}
	if _, err := tw.w.Write(b); err != nil {
		return err
	}
	if err := tw.w.WriteByte('\n'); err != nil {
		return err
	}
	return tw.w.Flush()
}

func (tw *jsonlWriter) Close() error {
	if tw == nil || tw.f == nil {
		return nil
	}
	tw.mu.Lock()
	defer tw.mu.Unlock()
	if tw.w != nil {
		_ = tw.w.Flush()
	}
	return tw.f.Close()
}

type siteCfg struct {
	PUE    float64 `json:"pue"`
	K      float64 `json:"k"`
	Region string  `json:"region"`
	CI     float64 `json:"ci"`
}

func main() {
	ns := env("WORKLOAD_NS", "workloads")
	tracePath := env("TRACE_PATH", "/var/log/ciw/decisions.jsonl")
	sitesPath := env("SITES_PATH", "/etc/ci-aware/sites.json")

	theta := types.Theta{ThetaE: 0.5, ThetaC: 0.5, Horizon: 2 * time.Hour, Alpha: 0.95, EgressCapMB: 500, ERef: 10, CRef: 5}
	if v := os.Getenv("FORECAST_BASE_URL"); v != "" {
		theta.ForecastBaseURL = v
	}
	deps := engine.Deps{
		Theta:   theta,
		Refs:    types.RefScales{ERef: theta.ERef, CRef: theta.CRef},
		Weights: types.Weights{E: theta.ThetaE, C: theta.ThetaC},
		Now:     time.Now,
	}
	if theta.ForecastBaseURL != "" {
		deps.CI = &providers.HTTPCIApi{BaseURL: theta.ForecastBaseURL, Client: &http.Client{Timeout: 3 * time.Second}}
	}

	sites, err := loadSites(sitesPath)
	if err != nil {
		log.Fatalf("load sites: %v", err)
	}

	tw, err := newJSONLWriter(tracePath)
	if err != nil {
		log.Fatalf("trace writer: %v", err)
	}
	defer func() { _ = tw.Close() }()

	cfg, err := rest.InClusterConfig()
	if err != nil {
		log.Fatalf("k8s cfg: %v", err)
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		log.Fatalf("k8s client: %v", err)
	}

	factory := informers.NewSharedInformerFactoryWithOptions(
		cs,
		0,
		informers.WithNamespace(ns),
		informers.WithTweakListOptions(func(lo *metav1.ListOptions) {
			lo.LabelSelector = labels.Set{"ciw/eligible": "true"}.String()
		}),
	)
	podInf := factory.Core().V1().Pods().Informer()

	handler := func(obj any) {
		pod, ok := obj.(*corev1.Pod)
		if !ok || pod == nil {
			return
		}
		if pod.Spec.NodeName != "" || pod.DeletionTimestamp != nil {
			return
		}
		if pod.Annotations != nil && pod.Annotations["ci-aware/scheduled"] == "true" {
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		nodes, err := snapshotNodes(ctx, cs, sites, ns)
		if err != nil {
			log.Printf("snapshot nodes: %v", err)
			return
		}
		job, err := extractJob(pod)
		if err != nil {
			log.Printf("extract job: %v", err)
			return
		}

		dec, trace, err := engine.Schedule(ctx, job, nodes, deps)
		if err != nil {
			log.Printf("schedule: %v (trace=%#v)", err, trace)
			_ = tw.WriteOne(trace)
			return
		}

		if err := bindPod(ctx, cs, pod, dec.NodeID); err != nil {
			log.Printf("bind pod: %v", err)
			return
		}

		siteName := siteForNode(nodes, dec.NodeID)
		if err := annotatePod(ctx, cs, pod, dec.NodeID, siteName); err != nil {
			log.Printf("annotate pod: %v", err)
		}

		trace.Node = dec.NodeID
		trace.Site = siteName
		trace.Fallback = false
		_ = tw.WriteOne(trace)
	}

	podInf.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    handler,
		UpdateFunc: func(_, newObj any) { handler(newObj) },
	})

	stop := make(chan struct{})
	factory.Start(stop)
	if ok := cache.WaitForCacheSync(stop, podInf.HasSynced); !ok {
		log.Fatalf("pod informer sync failed")
	}
	log.Printf("ci-aware controller running; namespace=%s", ns)
	<-stop
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func loadSites(path string) (map[string]siteCfg, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string]siteCfg{}
	if err := stdjson.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func snapshotNodes(ctx context.Context, cs *kubernetes.Clientset, sites map[string]siteCfg, namespace string) ([]types.NodeSnapshot, error) {
	pods, err := cs.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	type usage struct {
		cpu float64
		mem float64
	}
	used := map[string]usage{}
	for i := range pods.Items {
		p := &pods.Items[i]
		nodeName := p.Spec.NodeName
		if nodeName == "" {
			continue
		}
		phase := p.Status.Phase
		if phase == corev1.PodSucceeded || phase == corev1.PodFailed {
			continue
		}
		var cpu, mem float64
		sumResources := func(res corev1.ResourceList) {
			if res == nil {
				return
			}
			if v, ok := res[corev1.ResourceCPU]; ok {
				cpu += qtyToCores(v)
			}
			if v, ok := res[corev1.ResourceMemory]; ok {
				mem += qtyToGiB(v)
			}
		}
		for _, c := range p.Spec.InitContainers {
			sumResources(c.Resources.Requests)
			if cpu == 0 && mem == 0 {
				sumResources(c.Resources.Limits)
			}
		}
		for _, c := range p.Spec.Containers {
			sumResources(c.Resources.Requests)
			if cpu == 0 && mem == 0 {
				sumResources(c.Resources.Limits)
			}
		}
		if cpu == 0 && mem == 0 {
			continue
		}
		u := used[nodeName]
		u.cpu += cpu
		u.mem += mem
		used[nodeName] = u
	}

	lst, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]types.NodeSnapshot, 0, len(lst.Items))
	for i := range lst.Items {
		n := &lst.Items[i]
		labels := n.GetLabels()
		siteID := labels["site"]
		cfg, ok := sites[siteID]
		if !ok {
			continue
		}
		host := labels[corev1.LabelHostname]
		if host == "" {
			host = n.GetName()
		}
		alloc := n.Status.Allocatable
		snap := types.NodeSnapshot{
			ID:           host,
			Site:         types.SiteInfo{Name: siteID, Region: cfg.Region, PUE: cfg.PUE, K: cfg.K, CarbonIntensity: cfg.CI},
			AvailableCPU: qtyToCores(alloc[corev1.ResourceCPU]),
			AvailableGB:  qtyToGiB(alloc[corev1.ResourceMemory]),
			Labels:       labels,
			Metrics:      map[string]float64{},
		}
		if u, ok := used[host]; ok {
			snap.AvailableCPU = math.Max(0, snap.AvailableCPU-u.cpu)
			snap.AvailableGB = math.Max(0, snap.AvailableGB-u.mem)
		}
		out = append(out, snap)
	}
	return out, nil
}

func qtyToCores(q resource.Quantity) float64 {
	if mv := q.MilliValue(); mv > 0 {
		return float64(mv) / 1000.0
	}
	if v, ok := q.AsInt64(); ok && v > 0 {
		return float64(v)
	}
	return q.AsApproximateFloat64()
}

func qtyToGiB(q resource.Quantity) float64 {
	v := q.Value()
	if v <= 0 {
		return 0
	}
	return float64(v) / (1024.0 * 1024.0 * 1024.0)
}

func extractJob(pod *corev1.Pod) (types.Job, error) {
	if pod == nil {
		return types.Job{}, errors.New("nil pod")
	}
	var cpu, mem float64
	for _, c := range pod.Spec.Containers {
		if reqs := c.Resources.Requests; reqs != nil {
			cpu += qtyToCores(reqs[corev1.ResourceCPU])
			mem += qtyToGiB(reqs[corev1.ResourceMemory])
		}
	}
	tags := map[string]string{}
	if pod.Labels != nil {
		for k, v := range pod.Labels {
			tags[k] = v
		}
	}
	return types.Job{
		ID:       string(pod.UID),
		CPU:      cpu,
		MemoryGB: mem,
		Tags:     tags,
	}, nil
}

func bindPod(ctx context.Context, cs *kubernetes.Clientset, pod *corev1.Pod, node string) error {
	if pod == nil || node == "" {
		return errors.New("invalid bind args")
	}
	binding := &corev1.Binding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pod.Name,
			Namespace: pod.Namespace,
			UID:       pod.UID,
		},
		Target: corev1.ObjectReference{
			Kind: "Node",
			Name: node,
		},
	}
	err := cs.CoreV1().Pods(pod.Namespace).Bind(ctx, binding, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) || apierrors.IsInvalid(err) {
		return nil
	}
	return err
}

func annotatePod(ctx context.Context, cs *kubernetes.Clientset, pod *corev1.Pod, node, site string) error {
	annotations := map[string]string{
		"ci-aware/scheduled": "true",
		"ci-aware/node":      node,
	}
	if site != "" {
		annotations["ci-aware/site"] = site
	}

	payload := map[string]any{
		"metadata": map[string]any{
			"annotations": annotations,
		},
	}
	data, err := stdjson.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = cs.CoreV1().Pods(pod.Namespace).Patch(ctx, pod.Name, apitypes.MergePatchType, data, metav1.PatchOptions{})
	return err
}

func siteForNode(nodes []types.NodeSnapshot, nodeID string) string {
	for _, n := range nodes {
		if n.ID == nodeID {
			return n.Site.Name
		}
	}
	return ""
}
