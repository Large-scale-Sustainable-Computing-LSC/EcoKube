package candidate

import (
	"errors"
	"math"
	"time"

	"github.com/g-uva/EcoKube/kubenergysched/pkg/core"
	"github.com/g-uva/EcoKube/kubenergysched/pkg/metrics"
)

// Metrics captures feasibility and sustainability signals for a candidate node.
type Metrics struct {
	ID       string
	Feasible bool

	Carbon  float64
	Runtime float64
	Queue   float64
}

// Gather collects per-node metrics needed by sustainability-aware policies.
func Gather(job core.Job, nodes []core.SimulatedNode, now time.Time) ([]Metrics, error) {
	if len(nodes) == 0 {
		return nil, errors.New("candidate: no nodes provided")
	}
	if now.IsZero() {
		now = job.SubmitAt
	}
	if now.IsZero() {
		now = time.Unix(0, 0)
	}

	work := workloadFromJob(job)
	out := make([]Metrics, len(nodes))
	for i, n := range nodes {
		item := Metrics{
			ID:       pickNodeID(n),
			Feasible: n.CanAcceptJob(job),
		}
		if item.Feasible {
			nCopy := n
			item.Carbon = metrics.ComputeCICost(&nCopy, work, now)
			item.Runtime = runtimeSeconds(job)
			item.Queue = queueSeconds(&nCopy, now)
		}
		out[i] = item
	}
	return out, nil
}

// NormaliseMinMax rescales values to [0,1] using min-max scaling.
func NormaliseMinMax(vals []float64) []float64 {
	res := make([]float64, len(vals))
	if len(vals) == 0 {
		return res
	}
	minV := math.Inf(1)
	maxV := math.Inf(-1)
	for _, v := range vals {
		if v < minV {
			minV = v
		}
		if v > maxV {
			maxV = v
		}
	}
	if math.IsInf(minV, 1) || math.IsInf(maxV, -1) {
		for i := range res {
			res[i] = 0.5
		}
		return res
	}
	den := maxV - minV
	if den <= 1e-12 {
		for i := range res {
			res[i] = 0.5
		}
		return res
	}
	for i, v := range vals {
		res[i] = (v - minV) / den
	}
	return res
}

func workloadFromJob(j core.Job) core.Workload {
	dur := time.Duration(j.EstimatedDuration * float64(time.Second))
	if dur <= 0 {
		dur = time.Minute
	}
	return core.Workload{
		ID:         j.ID,
		SubmitTime: j.SubmitAt,
		Duration:   dur,
		CPU:        j.CPUReq,
		Memory:     j.MemReq,
		Labels:     j.Labels,
	}
}

func pickNodeID(n core.SimulatedNode) string {
	if n.Name != "" {
		return n.Name
	}
	return n.ID
}

func runtimeSeconds(job core.Job) float64 {
	if job.EstimatedDuration > 0 {
		return job.EstimatedDuration
	}
	return 60.0
}

func queueSeconds(n *core.SimulatedNode, now time.Time) float64 {
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
