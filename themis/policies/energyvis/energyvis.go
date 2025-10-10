package energyvis

import (
	"context"
	api "kube-scheduler/pkg/core"
)

type EnergyVis struct{ W struct{ Power, SCI, Util float64 } }
func (s *EnergyVis) Name() string { return "energyvis" }
func (s *EnergyVis) Score(ctx context.Context, job api.Job, nodes []api.Node) (api.Scores, error) {
    scores := api.Scores{}
    for _, n := range nodes {
        power := n.Metrics["node_power_w"]
        sci   := n.Metrics["sci_pred"]
        util  := (n.Metrics["cpu_used"]/n.CPUCap + n.Metrics["mem_used"]/n.MemCap)
        scores[n.ID] = s.W.Power*power + s.W.SCI*sci + s.W.Util*util
    }
    return scores, nil
}
func (s *EnergyVis) Select(sc api.Scores) (string, bool) { return api.ArgMin(sc) }
