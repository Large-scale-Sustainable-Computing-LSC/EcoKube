package ecovisor

import (
	"context"
	api "kube-scheduler/pkg/core"
)

type CarbonScaler struct{ Lambda float64 }
func (s *CarbonScaler) Name() string { return "carbonscaler" }
func (s *CarbonScaler) Score(ctx context.Context, job api.Job, nodes []api.Node) (api.Scores, error) {
    scores := api.Scores{}
    for _, n := range nodes {
        util := (n.Metrics["cpu_used"] / n.CPUCap) + (n.Metrics["mem_used"] / n.MemCap)
        ci   := n.Metrics["ci_norm"] // 0..1
        scores[n.ID] = util / (1.0 + s.Lambda*ci)
    }
    return scores, nil
}
func (s *CarbonScaler) Select(sc api.Scores) (string, bool) { return api.ArgMin(sc) }
