package k8sched

import (
	"context"
	"math"
	"time"

	"kube-scheduler/pkg/core"
)

type Policy struct{}

func (p *Policy) Name() string { return "k8" }

// Score matches the common interface: Job + []SimulatedNode.
// We adapt Job → Workload so CanAccept() works unchanged.
func (p *Policy) Score(_ context.Context, j core.Job, nodes []core.SimulatedNode) (core.Scores, error) {
	w := core.Workload{
		ID:         j.ID,
		CPU:        j.CPUReq,
		Memory:     j.MemReq,
		Duration:   time.Duration(j.EstimatedDuration * float64(time.Second)),
		SubmitTime: j.SubmitAt,
		Labels:     j.Labels,
	}

	sc := core.Scores{}
	for _, n := range nodes {
		if !n.CanAccept(w) {
			continue
		}
		used := 0.0
		if n.TotalCPU > 0 {
			used += (n.TotalCPU - n.AvailableCPU) / n.TotalCPU
		}
		if n.TotalMemory > 0 {
			used += (n.TotalMemory - n.AvailableMemory) / n.TotalMemory
		}
		sc[n.Name] = used // lower is better
	}
	if len(sc) == 0 {
		sc[""] = math.Inf(1)
	}
	return sc, nil
}

func (p *Policy) Select(sc core.Scores) (string, bool) { return core.ArgMin(sc) }
