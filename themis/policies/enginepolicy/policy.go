package enginepolicy

import (
    "context"
    "net/http"
    "os"
    "time"

    "github.com/g-uva/KubEnergySched/hermes/pkg/core"
    "github.com/g-uva/KubEnergySched/hermes/pkg/engine"
    "github.com/g-uva/KubEnergySched/hermes/pkg/providers"
    "github.com/g-uva/KubEnergySched/hermes/pkg/types"
)

// Config bridges simulator policy to the unified engine.
type Config struct {
    Theta types.Theta
    ERef  float64 // default from Theta.ERef if 0
    CRef  float64 // default from Theta.CRef if 0
}

type Policy struct {
    Cfg  Config
    deps engine.Deps
    last map[string]types.DecisionTrace // per-node traces from last Score
}

func (p *Policy) Name() string { return "engine" }

func (p *Policy) initDeps() {
    if p.deps.Weights.E != 0 || p.deps.Weights.C != 0 {
        return
    }
    th := p.Cfg.Theta
    // Allow env to override forecast base URL when running the simulator.
    if v := os.Getenv("FORECAST_BASE_URL"); v != "" {
        th.ForecastBaseURL = v
    }
    d := engine.Deps{
        CI:    nil,
        Theta: th,
        Refs:  types.RefScales{ERef: th.ERef, CRef: th.CRef},
        Weights: types.Weights{E: th.ThetaE, C: th.ThetaC},
        Now:   time.Now,
    }
    if p.Cfg.ERef > 0 { d.Refs.ERef = p.Cfg.ERef }
    if p.Cfg.CRef > 0 { d.Refs.CRef = p.Cfg.CRef }
    if th.ForecastBaseURL != "" {
        d.CI = &providers.HTTPCIApi{BaseURL: th.ForecastBaseURL, Client: &http.Client{Timeout: 3 * time.Second}}
    }
    p.deps = d
}

func (p *Policy) Score(ctx context.Context, j core.Job, nodes []core.SimulatedNode) (core.Scores, error) {
    p.initDeps()
    tj := types.Job{
        ID:       j.ID,
        CPU:      j.CPUReq,
        MemoryGB: j.MemReq,
        Deadline: time.Unix(0, 0), // optional
        Profile:  "",
        Tags:     j.Tags,
    }
    // Map simulated nodes → engine node snapshots
    snaps := make([]types.NodeSnapshot, 0, len(nodes))
    for _, n := range nodes {
        // Feasibility is re-checked in the engine
        site := types.SiteInfo{}
        if n.Site != nil {
            site = types.SiteInfo{
                Name:            n.Site.ID,
                Region:          n.Site.CIRegion,
                PUE:             n.Site.PUE,
                K:               n.Site.K,
                CarbonIntensity: n.Site.CarbonIntensity,
            }
        }
        snaps = append(snaps, types.NodeSnapshot{
            ID:           ifEmpty(n.Name, n.ID),
            Site:         site,
            AvailableCPU: n.AvailableCPU,
            AvailableGB:  n.AvailableMemory,
            Labels:       n.Labels,
            Metrics:      map[string]float64{},
        })
    }
    scores, traces := engine.ScoreNodes(ctx, tj, snaps, p.deps)
    p.last = traces
    // Convert to core.Scores keyed by node name
    out := core.Scores{}
    for id, v := range scores { out[id] = v }
    if len(out) == 0 { out[""] = 1e18 }
    return out, nil
}

func ifEmpty(a, b string) string { if a != "" { return a }; return b }

// Trace maps the engine's DecisionTrace to the simulator's DecisionTrace.
func (p *Policy) Trace(job core.Job, nodes []core.SimulatedNode, scores core.Scores, selected string) *core.DecisionTrace {
    if p.last == nil { return nil }
    t, ok := p.last[selected]
    if !ok { return nil }
    out := &core.DecisionTrace{
        JobID:        t.JobID,
        Site:         t.Site,
        Node:         t.Node,
        Etilde:       t.ENorm,
        Ctilde:       t.CNorm,
        Cost:         t.Cost,
        ThetaE:       t.ThetaE,
        ThetaC:       t.ThetaC,
        ForecastUsed: t.ForecastUsed,
        Fallback:     t.Fallback,
        RejectReason: t.RejectReason,
        QueuedAt:     t.QueuedAt,
        StartedAt:    t.StartedAt,
        EndedAt:      t.EndedAt,
    }
    return out
}
