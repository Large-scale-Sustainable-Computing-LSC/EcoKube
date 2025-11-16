package carbonscaler

import (
	"context"
	"math"
	"time"

	"github.com/g-uva/EcoKube/kubenergysched/pkg/core"
	"github.com/g-uva/EcoKube/kubenergysched/pkg/metrics"
)

type Config struct{ Lambda float64 }

type Policy struct{ Cfg Config }

func (p *Policy) Name() string { return "carbonscaler" }

func (p *Policy) Score(_ context.Context, j core.Job, nodes []core.SimulatedNode) (core.Scores, error) {
	w := core.Workload{
		ID: j.ID, CPU: j.CPUReq, Memory: j.MemReq,
		Duration:   time.Duration(j.EstimatedDuration * float64(time.Second)),
		SubmitTime: j.SubmitAt, Labels: j.Labels,
	}
	at := w.SubmitTime
	if at.IsZero() {
		at = time.Unix(0, 0)
	}

	type row struct {
		id     string
		carbon float64
		marg   float64
		queue  float64
		ok     bool
	}
	feats := make([]row, 0, len(nodes))
	minCarbon, maxCarbon := math.Inf(1), math.Inf(-1)
	minMarg, maxMarg := math.Inf(1), math.Inf(-1)
	maxQueue := 0.0
	for _, n := range nodes {
		if !n.CanAccept(w) {
			feats = append(feats, row{ok: false})
			continue
		}
		nCopy := n
		carbon := metrics.ComputeCICost(&nCopy, w, at)
		if carbon <= 0 {
			carbon = metrics.EstimateCarbonIntensity(&nCopy, at)
			if carbon <= 0 {
				carbon = 1
			}
		}
		queue := queueSeconds(&nCopy, at)
		if queue < 0 {
			queue = 0
		}
		if queue > maxQueue {
			maxQueue = queue
		}
		ci := metrics.EstimateCarbonIntensity(&nCopy, at)
		if ci <= 0 {
			ci = carbon
		}
		available := nCopy.AvailableCPU
		if available < 0 {
			available = 0
		}
		effective := available
		if effective > w.CPU {
			effective = w.CPU
		}
		marg := 0.0
		if ci > 0 {
			marg = effective / ci
		}
		if carbon < minCarbon {
			minCarbon = carbon
		}
		if carbon > maxCarbon {
			maxCarbon = carbon
		}
		if marg < minMarg {
			minMarg = marg
		}
		if marg > maxMarg {
			maxMarg = marg
		}
		feats = append(feats, row{id: n.Name, carbon: carbon, marg: marg, queue: queue, ok: true})
	}

	sc := core.Scores{}
	lambda := p.Cfg.Lambda
	if lambda < 0 {
		lambda = 0
	} else if lambda > 1 {
		lambda = 1
	}
	norm := func(v, lo, hi float64) float64 {
		if hi-lo <= 1e-9 {
			return 0.5
		}
		return (v - lo) / (hi - lo)
	}
	for _, r := range feats {
		if !r.ok {
			continue
		}
		carbonNorm := norm(r.carbon, minCarbon, maxCarbon)
		queueNorm := 0.0
		if maxQueue > 1e-9 {
			queueNorm = r.queue / maxQueue
		}
		score := lambda*carbonNorm + (1-lambda)*queueNorm
		sc[r.id] = score
	}
	if len(sc) == 0 {
		sc[""] = math.Inf(1)
	}
	return sc, nil
}

func (p *Policy) Select(sc core.Scores) (string, bool) { return core.ArgMin(sc) }

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

// Adapter methods
// func (s *Simulator) SetScheduleBatchSize(n int)  { s.base.SetScheduleBatchSize(n) }
// func (s *Simulator) AddWorkload(j core.Workload) { s.base.AddWorkload(j) }
// func (s *Simulator) Run()                        { s.base.Run() }
// func (s *Simulator) Logs() []core.LogEntry       { return s.base.Logs() }
