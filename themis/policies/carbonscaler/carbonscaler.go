package carbonscaler

import (
	"context"
	"time"
	"math"

	"kube-scheduler/pkg/core"
	"kube-scheduler/pkg/metrics"
)

type Config struct{ Lambda float64 }

type Policy struct{ Cfg Config }

func (p *Policy) Name() string { return "carbonscaler" }

func (p *Policy) Score(_ context.Context, j core.Job, nodes []core.SimulatedNode) (core.Scores, error) {
    w := core.Workload{
        ID: j.ID, CPU: j.CPUReq, Memory: j.MemReq,
        Duration: time.Duration(j.EstimatedDuration * float64(time.Second)),
        SubmitTime: j.SubmitAt, Labels: j.Labels,
    }
    now := time.Now()

    type row struct {
        id       string
        util     float64
        cicost   float64
        ok       bool
    }
    feats := make([]row, 0, len(nodes))
    for _, n := range nodes {
        if !n.CanAccept(w) {
            feats = append(feats, row{ok:false}); continue
        }
        used := 0.0
        if n.TotalCPU > 0 { used += (n.TotalCPU - n.AvailableCPU) / n.TotalCPU }
        if n.TotalMemory > 0 { used += (n.TotalMemory - n.AvailableMemory) / n.TotalMemory }
        // IMPORTANT: same CI model as logs/CI-Aware (includes PUE * k and time-varying profile)
        cic := metrics.ComputeCICost(&n, w, now)
        feats = append(feats, row{id:n.Name, util:used, cicost:cic, ok:true})
    }

    // Min–max normalise CI to 0..1 across candidates
    minC, maxC := math.Inf(1), math.Inf(-1)
    for _, r := range feats {
        if !r.ok { continue }
        if r.cicost < minC { minC = r.cicost }
        if r.cicost > maxC { maxC = r.cicost }
    }
    denom := maxC - minC
    sc := core.Scores{}
    for _, r := range feats {
        if !r.ok { continue }
        ciNorm := 0.0
        if denom > 1e-12 { ciNorm = (r.cicost - minC) / denom }
        sc[r.id] = r.util + p.Cfg.Lambda * ciNorm
    }
    if len(sc) == 0 { sc[""] = math.Inf(1) }
    return sc, nil
}

func (p *Policy) Select(sc core.Scores) (string, bool) { return core.ArgMin(sc) }


// Adapter methods
// func (s *Simulator) SetScheduleBatchSize(n int)  { s.base.SetScheduleBatchSize(n) }
// func (s *Simulator) AddWorkload(j core.Workload) { s.base.AddWorkload(j) }
// func (s *Simulator) Run()                        { s.base.Run() }
// func (s *Simulator) Logs() []core.LogEntry       { return s.base.Logs() }
