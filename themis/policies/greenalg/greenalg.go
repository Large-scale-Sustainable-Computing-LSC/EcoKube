package greenalg

import (
	"context"
	api "kube-scheduler/pkg/core"
)

type GreenAlgorithms struct{ W struct{ CI, Dur, Energy float64 } }
func (s *GreenAlgorithms) Name() string { return "green-algorithms" }
func (s *GreenAlgorithms) Score(ctx context.Context, job api.Job, nodes []api.Node) (api.Scores, error) {
    scores := api.Scores{}
    for _, n := range nodes {
        ci := n.Metrics["ci_g_per_kwh"]            // carbon intensity
        e  := n.Metrics["job_energy_kwh_pred"]     // predicted energy for this job on n
        d  := n.Metrics["job_duration_s_pred"]     // predicted duration on n
        sci := (e * ci) / (d + 1e-6)               // intensity per functional unit (sec)
        scores[n.ID] = s.W.CI*sci + s.W.Dur*d + s.W.Energy*e
    }
    return scores, nil
}
func (s *GreenAlgorithms) Select(sc api.Scores) (string, bool) { return api.ArgMin(sc) }
