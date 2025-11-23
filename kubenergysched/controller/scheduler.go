package main

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/g-uva/EcoKube/kespolicy/carbonscaler"
	"github.com/g-uva/EcoKube/kespolicy/ecokube"
	"github.com/g-uva/EcoKube/kubenergysched/pkg/core"
	"github.com/g-uva/EcoKube/kubenergysched/pkg/engine"
	"github.com/g-uva/EcoKube/kubenergysched/pkg/metrics"
	"github.com/g-uva/EcoKube/kubenergysched/pkg/types"
)

type scheduler interface {
	Name() string
	Schedule(ctx context.Context, job types.Job, snap *clusterSnapshot) (types.Decision, types.DecisionTrace, time.Duration, error)
}

type nodeView struct {
	Snapshot     types.NodeSnapshot
	Sim          core.SimulatedNode
	QueueSeconds float64
	CINorm       float64
}

type clusterSnapshot struct {
	Views         []nodeView
	NodeSnapshots []types.NodeSnapshot
	SimNodes      []core.SimulatedNode
	index         map[string]int
}

func (c *clusterSnapshot) ViewByID(id string) (nodeView, bool) {
	if c == nil {
		return nodeView{}, false
	}
	idx, ok := c.index[id]
	if !ok {
		return nodeView{}, false
	}
	return c.Views[idx], true
}

func (c *clusterSnapshot) ensureIndexes() {
	if c.index != nil {
		return
	}
	c.index = make(map[string]int, len(c.Views))
	for i, v := range c.Views {
		c.index[v.Snapshot.ID] = i
	}
}

func buildClusterSnapshot(views []nodeView) *clusterSnapshot {
	snap := &clusterSnapshot{Views: make([]nodeView, len(views))}
	copy(snap.Views, views)
	snap.NodeSnapshots = make([]types.NodeSnapshot, len(views))
	snap.SimNodes = make([]core.SimulatedNode, len(views))
	for i, v := range views {
		snap.NodeSnapshots[i] = v.Snapshot
		snap.SimNodes[i] = v.Sim
	}
	snap.ensureIndexes()
	return snap
}

func jobToCore(job types.Job) core.Job {
	return core.Job{
		ID:                job.ID,
		CPUReq:            job.CPU,
		MemReq:            job.MemoryGB,
		Tags:              job.Tags,
		EstimatedDuration: job.EstimatedDuration,
		SubmitAt:          job.SubmitTime,
	}
}

type ecoScheduler struct {
	policy *ecokube.Policy
	deps   engine.Deps
}

func newEcoScheduler(cfg ecokube.Config, deps engine.Deps) scheduler {
	pol := &ecokube.Policy{Mode: ecokube.ModeWeightedSum, Cfg: cfg, OverrideName: "ecokube"}
	return &ecoScheduler{policy: pol, deps: deps}
}

func (h *ecoScheduler) Name() string { return "ecokube" }

func (h *ecoScheduler) Schedule(ctx context.Context, job types.Job, snap *clusterSnapshot) (types.Decision, types.DecisionTrace, time.Duration, error) {
	if snap == nil || len(snap.Views) == 0 {
		return types.Decision{}, types.DecisionTrace{JobID: job.ID, Fallback: true, RejectReason: "no_nodes"}, 0, fmt.Errorf("ecokube: no nodes available")
	}
	scores, err := h.policy.Score(ctx, jobToCore(job), snap.SimNodes)
	if err != nil {
		return types.Decision{}, types.DecisionTrace{JobID: job.ID, Fallback: true, RejectReason: err.Error()}, 0, err
	}
	id, ok := core.ArgMin(scores)
	if !ok {
		return types.Decision{}, types.DecisionTrace{JobID: job.ID, Fallback: true, RejectReason: "no_candidate"}, 0, fmt.Errorf("ecokube: no candidate")
	}
	engScores, traces := engine.ScoreNodes(ctx, job, snap.NodeSnapshots, h.deps)
	_ = engScores
	trace := traces[id]
	trace.Scheduler = h.Name()
	if view, ok := snap.ViewByID(id); ok {
		trace.QueueSeconds = view.QueueSeconds
		if trace.Site == "" {
			trace.Site = view.Snapshot.Site.Name
		}
	}
	dec := types.Decision{NodeID: id, Scale: 1}
	trace.Scale = dec.Scale
	return dec, trace, 0, nil
}

type carbonScheduler struct {
	policy      *carbonscaler.Policy
	deps        engine.Deps
	shiftFrac   float64
	elasticity  float64
	deferThresh float64
}

func newCarbonScheduler(pol *carbonscaler.Policy, deps engine.Deps, shiftFrac, elasticity, deferThresh float64) scheduler {
	if shiftFrac < 0 {
		shiftFrac = 0
	}
	if elasticity < 0 {
		elasticity = 0
	}
	if deferThresh <= 0 {
		deferThresh = 0.5
	}
	return &carbonScheduler{
		policy:      pol,
		deps:        deps,
		shiftFrac:   shiftFrac,
		elasticity:  elasticity,
		deferThresh: deferThresh,
	}
}

func (c *carbonScheduler) Name() string { return "carbonscaler" }

func (c *carbonScheduler) Schedule(ctx context.Context, job types.Job, snap *clusterSnapshot) (types.Decision, types.DecisionTrace, time.Duration, error) {
	if snap == nil || len(snap.Views) == 0 {
		return types.Decision{}, types.DecisionTrace{JobID: job.ID, Fallback: true, RejectReason: "no_nodes"}, 0, fmt.Errorf("carbonscaler: no nodes")
	}
	scores, err := c.policy.Score(ctx, jobToCore(job), snap.SimNodes)
	if err != nil {
		return types.Decision{}, types.DecisionTrace{JobID: job.ID, Fallback: true, RejectReason: err.Error()}, 0, err
	}
	id, ok := core.ArgMin(scores)
	if !ok {
		return types.Decision{}, types.DecisionTrace{JobID: job.ID, Fallback: true, RejectReason: "no_candidate"}, 0, fmt.Errorf("carbonscaler: no candidate")
	}
	engScores, traces := engine.ScoreNodes(ctx, job, snap.NodeSnapshots, c.deps)
	_ = engScores
	trace := traces[id]
	trace.Scheduler = c.Name()
	view, haveView := snap.ViewByID(id)
	if haveView {
		trace.QueueSeconds = view.QueueSeconds
		if trace.Site == "" {
			trace.Site = view.Snapshot.Site.Name
		}
	}
	queueDur := time.Duration(0)
	if haveView && view.QueueSeconds > 0 {
		queueDur = time.Duration(view.QueueSeconds * float64(time.Second))
	}
	var deferFor time.Duration
	if queueDur > 0 {
		maxDefer := time.Duration(job.SlackSeconds * float64(time.Second))
		if maxDefer <= 0 && job.EstimatedDuration > 0 && c.shiftFrac > 0 {
			maxDefer = time.Duration(job.EstimatedDuration * c.shiftFrac * float64(time.Second))
		}
		if maxDefer > 0 && queueDur <= maxDefer {
			cin := view.CINorm
			if cin >= c.deferThresh {
				deferFor = queueDur
			}
		}
	}
	if deferFor > 0 {
		trace.Fallback = true
		trace.DeferredFor = deferFor.Seconds()
		trace.RejectReason = fmt.Sprintf("deferred_%.0fs", deferFor.Seconds())
		return types.Decision{}, trace, deferFor, nil
	}
	dec := types.Decision{NodeID: id, Scale: 1}
	if haveView && c.elasticity > 0 {
		factor := 1.0 + c.elasticity*(1.0-view.CINorm)
		if factor < 1 {
			factor = 1
		}
		scale := int(math.Round(factor))
		if scale < 1 {
			scale = 1
		}
		dec.Scale = scale
	}
	if dec.Scale < 1 {
		dec.Scale = 1
	}
	trace.Scale = dec.Scale
	return dec, trace, 0, nil
}

type k8sScheduler struct {
	deps engine.Deps
}

func newK8sScheduler(deps engine.Deps) scheduler {
	return &k8sScheduler{deps: deps}
}

func (k *k8sScheduler) Name() string { return "k8s" }

func (k *k8sScheduler) Schedule(ctx context.Context, job types.Job, snap *clusterSnapshot) (types.Decision, types.DecisionTrace, time.Duration, error) {
	if snap == nil || len(snap.Views) == 0 {
		return types.Decision{}, types.DecisionTrace{JobID: job.ID, Fallback: true, RejectReason: "no_nodes"}, 0, fmt.Errorf("k8s: no nodes")
	}
	scores, traces := engine.ScoreNodes(ctx, job, snap.NodeSnapshots, k.deps)
	if len(scores) == 0 {
		return types.Decision{}, types.DecisionTrace{JobID: job.ID, Fallback: true, RejectReason: "no_candidate"}, 0, fmt.Errorf("k8s: no candidate")
	}

	// Pick the feasible node with the smallest queue length (closest to default scheduler behaviour).
	bestID := ""
	bestQueue := math.MaxFloat64
	for _, v := range snap.Views {
		if _, ok := scores[v.Snapshot.ID]; !ok {
			continue
		}
		if v.QueueSeconds < bestQueue {
			bestQueue = v.QueueSeconds
			bestID = v.Snapshot.ID
		}
	}
	if bestID == "" {
		return types.Decision{}, types.DecisionTrace{JobID: job.ID, Fallback: true, RejectReason: "no_candidate"}, 0, fmt.Errorf("k8s: no candidate")
	}

	trace := traces[bestID]
	trace.Scheduler = k.Name()
	dec := types.Decision{NodeID: bestID, Scale: 1}
	trace.Scale = dec.Scale
	if snap != nil {
		if view, ok := snap.ViewByID(bestID); ok {
			if trace.Site == "" {
				trace.Site = view.Snapshot.Site.Name
			}
			trace.QueueSeconds = view.QueueSeconds
		}
	}
	return dec, trace, 0, nil
}

func queueSecondsForNode(n *core.SimulatedNode, now time.Time) float64 {
	if n == nil {
		return 0
	}
	next := n.NextReleaseAfter(now)
	if next.IsZero() {
		return 0
	}
	wait := next.Sub(now).Seconds()
	if wait < 0 {
		return 0
	}
	return wait
}

func cinormForNode(n *core.SimulatedNode, now time.Time) float64 {
	if n == nil {
		return 0.5
	}
	norm := n.CurrentCINorm(now)
	if norm <= 0 {
		est := metrics.EstimateCarbonIntensity(n, now)
		norm = cinormForLabels(n.Labels, est)
	}
	if norm < 0 {
		norm = 0
	} else if norm > 1 {
		norm = 1
	}
	return norm
}
