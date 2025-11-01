package main

import (
	"context"
	stdjson "encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/g-uva/KubEnergySched/kespolicy/carbonscaler"
	"github.com/g-uva/KubEnergySched/kespolicy/hetpolicy"
	core "github.com/g-uva/KubEnergySched/kubenergysched/pkg/core"
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
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

type jsonlWriter struct {
	mu sync.Mutex
	w  *os.File
}

func newJSONLWriter(path string) (*jsonlWriter, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	return &jsonlWriter{w: f}, nil
}

func (tw *jsonlWriter) WriteOne(v any) error {
	if tw == nil || tw.w == nil {
		return nil
	}
	tw.mu.Lock()
	defer tw.mu.Unlock()
	enc := stdjson.NewEncoder(tw.w)
	if err := enc.Encode(v); err != nil {
		return err
	}
	return tw.w.Sync()
}

func (tw *jsonlWriter) Close() error {
	if tw == nil || tw.w == nil {
		return nil
	}
	tw.mu.Lock()
	defer tw.mu.Unlock()
	return tw.w.Close()
}

type siteCfg struct {
	PUE    float64 `json:"pue"`
	K      float64 `json:"k"`
	Region string  `json:"region"`
	CI     float64 `json:"ci"`
}

type controller struct {
	namespace string
	runID     string
	sites     map[string]siteCfg
	cs        *kubernetes.Clientset
	scheduler scheduler
	writer    *jsonlWriter

	podLister   corelisters.PodLister
	podInformer cache.SharedIndexInformer
	queue       workqueue.RateLimitingInterface

	stateMu sync.Mutex
	states  map[string]*podState

	now func() time.Time
}

type podState struct {
	Trace         types.DecisionTrace
	StartRecorded bool
	EndRecorded   bool
}

func main() {
	ns := env("WORKLOAD_NS", "workloads")
	tracePath := env("TRACE_PATH", "/var/log/ciw/decisions.jsonl")
	sitesPath := env("SITES_PATH", "/etc/ci-aware/sites.json")
	runID := os.Getenv("TRACE_RUN_ID")
	policyName := env("SCHEDULER_POLICY", "hetpolicy")

	theta := types.Theta{
		ThetaE:      0.58,
		ThetaC:      0.42,
		Horizon:     2 * time.Hour,
		Alpha:       0.95,
		EgressCapMB: 500,
		ERef:        10,
		CRef:        5,
	}
	deps := engine.Deps{
		Theta:   theta,
		Refs:    types.RefScales{ERef: theta.ERef, CRef: theta.CRef},
		Weights: types.Weights{E: theta.ThetaE, C: theta.ThetaC},
		Now:     time.Now,
	}
	if base := os.Getenv("FORECAST_BASE_URL"); base != "" {
		deps.CI = &providers.HTTPCIApi{BaseURL: base, Client: &http.Client{Timeout: 3 * time.Second}}
		deps.Theta.ForecastBaseURL = base
	}

	sites, err := loadSites(sitesPath)
	if err != nil {
		log.Fatalf("load sites: %v", err)
	}

	writer, err := newJSONLWriter(tracePath)
	if err != nil {
		log.Fatalf("trace writer: %v", err)
	}
	defer func() { _ = writer.Close() }()

	cfg, err := rest.InClusterConfig()
	if err != nil {
		log.Fatalf("k8s cfg: %v", err)
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		log.Fatalf("k8s client: %v", err)
	}

	sched, err := buildScheduler(policyName, deps)
	if err != nil {
		log.Fatalf("scheduler: %v", err)
	}

	factory := informers.NewSharedInformerFactoryWithOptions(
		cs,
		0,
		informers.WithNamespace(ns),
		informers.WithTweakListOptions(func(lo *metav1.ListOptions) {
			lo.LabelSelector = labels.Set{"ciw/eligible": "true"}.String()
		}),
	)
	podInformer := factory.Core().V1().Pods().Informer()

	ctrl := &controller{
		namespace:   ns,
		runID:       runID,
		sites:       sites,
		cs:          cs,
		scheduler:   sched,
		writer:      writer,
		podLister:   factory.Core().V1().Pods().Lister(),
		podInformer: podInformer,
		queue:       workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "ciw-pods"),
		states:      map[string]*podState{},
		now:         time.Now,
	}

	podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			if pod, ok := obj.(*corev1.Pod); ok {
				ctrl.observePod(pod)
				ctrl.enqueueIfNeeded(pod)
			}
		},
		UpdateFunc: func(_, newObj any) {
			if pod, ok := newObj.(*corev1.Pod); ok {
				ctrl.observePod(pod)
				ctrl.enqueueIfNeeded(pod)
			}
		},
		DeleteFunc: func(obj any) {
			if pod, ok := obj.(*corev1.Pod); ok {
				ctrl.dropState(pod)
			}
		},
	})

	stopCh := make(chan struct{})
	factory.Start(stopCh)
	if ok := cache.WaitForCacheSync(stopCh, podInformer.HasSynced); !ok {
		log.Fatalf("pod informer sync failed")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ctrl.runWorkers(ctx, 2)

	log.Printf("ci-aware controller running; namespace=%s policy=%s", ns, sched.Name())

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Printf("shutdown signal received")
	cancel()
	ctrl.queue.ShutDown()
	close(stopCh)
}

func (c *controller) runWorkers(ctx context.Context, workers int) {
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for c.processNextItem(ctx) {
			}
		}()
	}
	wg.Wait()
}

func (c *controller) processNextItem(ctx context.Context) bool {
	obj, shutdown := c.queue.Get()
	if shutdown {
		return false
	}
	defer c.queue.Done(obj)

	key, ok := obj.(string)
	if !ok {
		c.queue.Forget(obj)
		return true
	}
	ns, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		c.queue.Forget(obj)
		return true
	}
	pod, err := c.podLister.Pods(ns).Get(name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			c.queue.Forget(obj)
			return true
		}
		log.Printf("get pod %s: %v", key, err)
		c.queue.AddRateLimited(obj)
		return true
	}
	if !shouldSchedule(pod) {
		c.queue.Forget(obj)
		return true
	}

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	deferFor, err := c.schedulePod(ctx, pod.DeepCopy())
	if err != nil {
		log.Printf("schedule %s: %v", key, err)
		c.queue.AddRateLimited(obj)
		return true
	}
	c.queue.Forget(obj)
	if deferFor > 0 {
		c.queue.AddAfter(key, deferFor)
	}
	return true
}

func shouldSchedule(pod *corev1.Pod) bool {
	if pod == nil {
		return false
	}
	if pod.DeletionTimestamp != nil {
		return false
	}
	if pod.Spec.NodeName != "" {
		return false
	}
	if pod.Annotations != nil && pod.Annotations["ci-aware/scheduled"] == "true" {
		return false
	}
	return true
}

func (c *controller) enqueueIfNeeded(pod *corev1.Pod) {
	if !shouldSchedule(pod) {
		return
	}
	key, err := cache.MetaNamespaceKeyFunc(pod)
	if err != nil {
		return
	}
	c.queue.Add(key)
}

func (c *controller) schedulePod(ctx context.Context, pod *corev1.Pod) (time.Duration, error) {
	job, err := extractJob(pod)
	if err != nil {
		return 0, err
	}
	snap, err := c.snapshot(ctx)
	if err != nil {
		return 0, err
	}
	if snap == nil || len(snap.Views) == 0 {
		trace := types.DecisionTrace{JobID: job.ID, Scheduler: c.scheduler.Name(), Source: "kubernetes", Fallback: true, RejectReason: "no_nodes", QueuedAt: job.SubmitTime}
		if c.runID != "" {
			trace.RunID = c.runID
		}
		_ = c.writer.WriteOne(trace)
		return 0, errors.New("no candidate nodes")
	}

	dec, trace, deferFor, err := c.scheduler.Schedule(ctx, job, snap)
	trace.JobID = defaultString(trace.JobID, job.ID)
	trace.ResultType = defaultString(trace.ResultType, "kub_result")
	trace.Source = defaultString(trace.Source, "kubernetes")
	trace.Scheduler = c.scheduler.Name()
	if c.runID != "" && trace.RunID == "" {
		trace.RunID = c.runID
	}
	if trace.QueuedAt.IsZero() {
		trace.QueuedAt = job.SubmitTime
	}
	if trace.Scale == 0 {
		trace.Scale = dec.Scale
	}

	if deferFor > 0 {
		trace.Fallback = true
		trace.DeferredFor = deferFor.Seconds()
		_ = c.writer.WriteOne(trace)
		return deferFor, nil
	}
	if err != nil {
		trace.Fallback = true
		if trace.RejectReason == "" {
			trace.RejectReason = err.Error()
		}
		_ = c.writer.WriteOne(trace)
		return 0, err
	}

	view, ok := snap.ViewByID(dec.NodeID)
	if !ok {
		trace.Fallback = true
		trace.RejectReason = "selected_node_missing"
		_ = c.writer.WriteOne(trace)
		return 0, fmt.Errorf("selected node %s not found", dec.NodeID)
	}

	if err := bindPod(ctx, c.cs, pod, dec.NodeID); err != nil {
		return 0, err
	}

	if err := annotatePod(ctx, c.cs, pod, dec.NodeID, view.Snapshot.Site.Name, trace, c.runID); err != nil {
		log.Printf("annotate pod: %v", err)
	}

	trace.Node = dec.NodeID
	trace.Site = view.Snapshot.Site.Name
	trace.Scale = dec.Scale
	_ = c.writer.WriteOne(trace)

	c.recordState(pod, trace)
	return 0, nil
}

func (c *controller) snapshot(ctx context.Context) (*clusterSnapshot, error) {
	now := c.now()
	pods, err := c.cs.CoreV1().Pods(c.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	type usage struct {
		cpu float64
		mem float64
	}
	used := map[string]usage{}
	reservations := map[string][]core.Reservation{}
	for i := range pods.Items {
		p := &pods.Items[i]
		node := p.Spec.NodeName
		if node == "" {
			continue
		}
		phase := p.Status.Phase
		if phase == corev1.PodSucceeded || phase == corev1.PodFailed {
			continue
		}
		cpu, mem := podResources(p)
		if cpu == 0 && mem == 0 {
			continue
		}
		u := used[node]
		u.cpu += cpu
		u.mem += mem
		used[node] = u

		dur := podDuration(p)
		if dur <= 0 {
			dur = time.Minute
		}
		start := podStartTime(p)
		if start.IsZero() {
			start = p.CreationTimestamp.Time
		}
		reservations[node] = append(reservations[node], core.Reservation{End: start.Add(dur), CPU: cpu, Mem: mem})
	}

	lst, err := c.cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	siteCache := make(map[string]*core.Site)
	views := make([]nodeView, 0, len(lst.Items))
	for i := range lst.Items {
		n := &lst.Items[i]
		labels := n.GetLabels()
		siteID := labels["site"]
		cfg, ok := c.sites[siteID]
		if !ok {
			continue
		}
		site := siteCache[siteID]
		if site == nil {
			site = &core.Site{ID: siteID, PUE: cfg.PUE, K: cfg.K, CIRegion: cfg.Region, CarbonIntensity: cfg.CI}
			siteCache[siteID] = site
		}
		alloc := n.Status.Allocatable
		totalCPU := qtyToCores(alloc[corev1.ResourceCPU])
		totalMem := qtyToGiB(alloc[corev1.ResourceMemory])
		u := used[n.Name]
		availCPU := math.Max(0, totalCPU-u.cpu)
		availMem := math.Max(0, totalMem-u.mem)
		siteInfo := types.SiteInfo{Name: siteID, Region: cfg.Region, PUE: cfg.PUE, K: cfg.K, CarbonIntensity: cfg.CI}
		labelsCopy := make(map[string]string, len(labels))
		for k, v := range labels {
			labelsCopy[k] = v
		}
		snap := types.NodeSnapshot{
			ID:           n.Name,
			Site:         siteInfo,
			AvailableCPU: availCPU,
			AvailableGB:  availMem,
			Labels:       labelsCopy,
			Metrics:      map[string]float64{},
		}
		sim := core.SimulatedNode{
			ID:              n.Name,
			Name:            n.Name,
			TotalCPU:        totalCPU,
			TotalMemory:     totalMem,
			AvailableCPU:    availCPU,
			AvailableMemory: availMem,
			CarbonIntensity: cfg.CI,
			Labels:          labelsCopy,
			SiteID:          siteID,
			Site:            site,
		}
		sim.Reservations = append(sim.Reservations, reservations[n.Name]...)
		queue := queueSecondsForNode(&sim, now)
		cinorm := cinormForNode(&sim, now)
		snap.Metrics["queue_seconds"] = queue
		snap.Metrics["ci_norm"] = cinorm
		if v, ok := labels["power_w_mean"]; ok {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				snap.Metrics["power_w_mean"] = f
			}
		}
		views = append(views, nodeView{Snapshot: snap, Sim: sim, QueueSeconds: queue, CINorm: cinorm})
	}
	return buildClusterSnapshot(views), nil
}

func (c *controller) recordState(pod *corev1.Pod, trace types.DecisionTrace) {
	if pod == nil {
		return
	}
	key := string(pod.UID)
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	st := c.states[key]
	if st == nil {
		st = &podState{}
		c.states[key] = st
	}
	st.Trace = trace
	if st.Trace.JobID == "" {
		st.Trace.JobID = string(pod.UID)
	}
	if st.Trace.QueuedAt.IsZero() {
		st.Trace.QueuedAt = pod.CreationTimestamp.Time
	}
}

func (c *controller) observePod(pod *corev1.Pod) {
	if pod == nil {
		return
	}
	if pod.Annotations == nil || pod.Annotations["ci-aware/scheduled"] != "true" {
		return
	}
	key := string(pod.UID)
	c.stateMu.Lock()
	st := c.states[key]
	if st == nil {
		st = &podState{Trace: types.DecisionTrace{JobID: string(pod.UID), Scheduler: c.scheduler.Name(), Source: "kubernetes"}}
		c.states[key] = st
	}
	if st.Trace.JobID == "" {
		st.Trace.JobID = string(pod.UID)
	}
	if st.Trace.QueuedAt.IsZero() {
		st.Trace.QueuedAt = pod.CreationTimestamp.Time
	}
	start := podStartTime(pod)
	end := podCompletionTime(pod)
	changed := false
	if !start.IsZero() && st.Trace.StartedAt.IsZero() {
		st.Trace.StartedAt = start
		st.StartRecorded = true
		changed = true
	}
	if !end.IsZero() && st.Trace.EndedAt.IsZero() {
		st.Trace.EndedAt = end
		st.EndRecorded = true
		changed = true
	}
	st.Trace.Node = pod.Spec.NodeName
	if pod.Annotations != nil {
		if site := pod.Annotations["ci-aware/site"]; site != "" {
			st.Trace.Site = site
		}
	}
	st.Trace.Scheduler = c.scheduler.Name()
	st.Trace.Source = "kubernetes"
	c.stateMu.Unlock()

	if changed {
		_ = c.writer.WriteOne(st.Trace)
	}
	if !end.IsZero() {
		c.dropState(pod)
	}
}

func (c *controller) dropState(pod *corev1.Pod) {
	if pod == nil {
		return
	}
	key := string(pod.UID)
	c.stateMu.Lock()
	delete(c.states, key)
	c.stateMu.Unlock()
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
		Target: corev1.ObjectReference{Kind: "Node", Name: node},
	}
	err := cs.CoreV1().Pods(pod.Namespace).Bind(ctx, binding, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) || apierrors.IsInvalid(err) {
		return nil
	}
	return err
}

func annotatePod(ctx context.Context, cs *kubernetes.Clientset, pod *corev1.Pod, node, site string, trace types.DecisionTrace, runID string) error {
	annotations := map[string]string{
		"ci-aware/scheduled": "true",
		"ci-aware/node":      node,
		"ci-aware/scheduler": trace.Scheduler,
	}
	if site != "" {
		annotations["ci-aware/site"] = site
	}
	if runID != "" {
		annotations["ci-aware/run-id"] = runID
	}
	if !trace.QueuedAt.IsZero() {
		annotations["ci-aware/queued-at"] = trace.QueuedAt.UTC().Format(time.RFC3339Nano)
	}
	if trace.Scale > 0 {
		annotations["ci-aware/scale"] = strconv.Itoa(trace.Scale)
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

func podResources(pod *corev1.Pod) (float64, float64) {
	var cpu, mem float64
	add := func(res corev1.ResourceList) {
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
	for _, c := range pod.Spec.InitContainers {
		add(c.Resources.Requests)
		if cpu == 0 && mem == 0 {
			add(c.Resources.Limits)
		}
	}
	for _, c := range pod.Spec.Containers {
		add(c.Resources.Requests)
		if cpu == 0 && mem == 0 {
			add(c.Resources.Limits)
		}
	}
	return cpu, mem
}

func podDuration(pod *corev1.Pod) time.Duration {
	if pod == nil || pod.Annotations == nil {
		return 0
	}
	if v := pod.Annotations["ciw/duration_req_s"]; v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			return time.Duration(f * float64(time.Second))
		}
	}
	return 0
}

func podStartTime(pod *corev1.Pod) time.Time {
	if pod == nil {
		return time.Time{}
	}
	if pod.Status.StartTime != nil {
		return pod.Status.StartTime.Time
	}
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Running != nil {
			return cs.State.Running.StartedAt.Time
		}
		if cs.LastTerminationState.Terminated != nil {
			return cs.LastTerminationState.Terminated.StartedAt.Time
		}
	}
	return time.Time{}
}

func podCompletionTime(pod *corev1.Pod) time.Time {
	if pod == nil {
		return time.Time{}
	}
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Terminated != nil {
			return cs.State.Terminated.FinishedAt.Time
		}
		if cs.LastTerminationState.Terminated != nil {
			return cs.LastTerminationState.Terminated.FinishedAt.Time
		}
	}
	return time.Time{}
}

func extractJob(pod *corev1.Pod) (types.Job, error) {
	if pod == nil {
		return types.Job{}, errors.New("nil pod")
	}
	cpu, mem := podResources(pod)
	jobID := string(pod.UID)
	if pod.Labels != nil {
		if v := pod.Labels["ciw/workload_id"]; v != "" {
			jobID = v
		}
	}
	duration := 0.0
	if pod.Annotations != nil {
		if v := pod.Annotations["ciw/duration_req_s"]; v != "" {
			if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
				duration = f
			}
		}
	}
	tags := map[string]string{}
	if pod.Labels != nil {
		for k, v := range pod.Labels {
			tags[k] = v
		}
	}
	slack := 0.0
	if pod.Annotations != nil {
		if v := pod.Annotations["ciw/max_defer_s"]; v != "" {
			if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 {
				slack = f
			}
		}
	}
	return types.Job{
		ID:                jobID,
		CPU:               cpu,
		MemoryGB:          mem,
		Tags:              tags,
		EstimatedDuration: duration,
		SubmitTime:        pod.CreationTimestamp.Time,
		SlackSeconds:      slack,
	}, nil
}

func defaultString(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func buildScheduler(policy string, deps engine.Deps) (scheduler, error) {
	switch policyName := normalizePolicy(policy); policyName {
	case "hetpolicy":
		cfg := hetpolicy.DefaultConfig()
		cfg.Alpha = 0.58
		cfg.Beta = 0.21
		cfg.Gamma = 0.21
		cfg.Now = deps.Now
		return newHetScheduler(cfg, deps), nil
	case "carbonscaler":
		lambda := parseEnvFloat("CARBONSCALER_LAMBDA", 0.55)
		shift := parseEnvFloat("CARBONSCALER_SHIFT_FRACTION", 0.3)
		elasticity := parseEnvFloat("CARBONSCALER_ELASTICITY", 1.0)
		threshold := parseEnvFloat("CARBONSCALER_DEFER_THRESHOLD", 0.6)
		pol := &carbonscaler.Policy{Cfg: carbonscaler.Config{Lambda: lambda}}
		return newCarbonScheduler(pol, deps, shift, elasticity, threshold), nil
	default:
		return nil, fmt.Errorf("unknown scheduler policy %q", policy)
	}
}

func normalizePolicy(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "het", "hetpolicy":
		return "hetpolicy"
	case "carbonscaler", "carbon", "carbon-scaler":
		return "carbonscaler"
	default:
		return strings.ToLower(s)
	}
}

func parseEnvFloat(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

func cinormForLabels(labels map[string]string, fallback float64) float64 {
	switch labels["ci_profile"] {
	case "low":
		return 0.2
	case "medium":
		return 0.5
	case "high":
		return 0.8
	}
	if fallback > 0 {
		v := (fallback - 50.0) / 650.0
		if v < 0 {
			v = 0
		}
		if v > 1 {
			v = 1
		}
		return v
	}
	return 0.5
}
