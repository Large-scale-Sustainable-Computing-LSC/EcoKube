package hetsched

import (
	"context"
	"math"
	"time"

	"github.com/g-uva/EcoKube/hetsched/pkg/core"
	"github.com/g-uva/EcoKube/hetsched/pkg/metrics"
)

// Weights mirrors the original HETSCHED axes: carbon, waiting time, utilisation.
type Weights struct {
	Carbon float64
	Wait   float64
	Util   float64
}

// Policy implements a lightly-parameterised baseline similar to the earlier HETSCHED prototype.
type Policy struct {
	W         Weights
	AlphaMass float64
	Lookahead time.Duration
}

func (p *Policy) Name() string { return "hetsched" }

func (p *Policy) Score(_ context.Context, j core.Job, nodes []core.SimulatedNode) (core.Scores, error) {
	if len(nodes) == 0 {
		return core.Scores{}, nil
	}
	now := j.SubmitAt
	if now.IsZero() {
		now = time.Unix(0, 0)
	}
	if p.Lookahead > 0 {
		now = now.Add(p.Lookahead)
	}
	work := core.Workload{
		ID:         j.ID,
		CPU:        j.CPUReq,
		Memory:     j.MemReq,
		Duration:   time.Duration(j.EstimatedDuration * float64(time.Second)),
		SubmitTime: j.SubmitAt,
		Labels:     j.Labels,
	}

	scores := core.Scores{}
	for i := range nodes {
		n := &nodes[i]
		if !n.CanAccept(work) {
			continue
		}
		util := nodeUtilisation(n)
		ci := metrics.ComputeCICost(n, work, now)
		wait := queueSeconds(n, now)

		carbonWeight := p.W.Carbon
		if p.AlphaMass > 0 && work.Duration > time.Hour {
			carbonWeight *= 1 + p.AlphaMass
		}
		score := carbonWeight*ci + p.W.Wait*wait + p.W.Util*util
		scores[nodeID(*n)] = score
	}
	if len(scores) == 0 {
		scores[""] = math.Inf(1)
	}
	return scores, nil
}

func nodeID(n core.SimulatedNode) string {
	if n.Name != "" {
		return n.Name
	}
	return n.ID
}

func nodeUtilisation(n *core.SimulatedNode) float64 {
	totalCPU := n.TotalCPU
	totalMem := n.TotalMemory
	if totalCPU <= 0 && totalMem <= 0 {
		return 0
	}
	cpuUsed := 0.0
	if totalCPU > 0 {
		cpuUsed = (totalCPU - n.AvailableCPU) / totalCPU
	}
	memUsed := 0.0
	if totalMem > 0 {
		memUsed = (totalMem - n.AvailableMemory) / totalMem
	}
	return cpuUsed + memUsed
}

func queueSeconds(n *core.SimulatedNode, at time.Time) float64 {
	if n == nil {
		return 0
	}
	next := n.NextReleaseAfter(at)
	if next.IsZero() {
		return 0
	}
	wait := next.Sub(at).Seconds()
	if wait < 0 {
		return 0
	}
	return wait
}
