package cisched

import (
	"context"
	"math"
	"time"

	"kube-scheduler/pkg/core"
	"kube-scheduler/pkg/metrics"
)

// Score implements the CI-Aware scorer with adaptive carbon, convex penalty,
// optional look-ahead, and damped wait term.
func (p *Policy) Score(ctx context.Context, j core.Job, nodes []core.SimulatedNode) (core.Scores, error) {
	w := core.Workload{
		ID:         j.ID,
		CPU:        j.CPUReq,
		Memory:     j.MemReq,
		Duration:   time.Duration(j.EstimatedDuration * float64(time.Second)),
		SubmitTime: j.SubmitAt,
		Labels:     j.Labels,
	}
	now := time.Now()

	type feat struct {
		id        string
		ok        bool
		ciCostG   float64
		waitS     float64
		utilProxy float64
		mass      float64
		nodeIdx   int // if you want look-ahead with specific node, keep index
	}
	feats := make([]feat, 0, len(nodes))
	for idx, n := range nodes {
		if !n.CanAccept(w) {
			feats = append(feats, feat{id: n.Name, ok: false})
			continue
		}
		ciNow := metrics.ComputeCICost(&n, w, now)
		if p.Lookahead > 0 {
			t2 := now.Add(p.Lookahead)
			ciFuture := metrics.ComputeCICost(&n, w, t2)
			if ciFuture < ciNow {
				ciNow = ciFuture
			}
		}
		waitS := 0.0
		if t := n.NextReleaseAfter(now); !t.IsZero() && t.After(now) {
			waitS = t.Sub(now).Seconds()
		}
		used := 0.0
		if n.TotalCPU > 0 {
			used += (n.TotalCPU - n.AvailableCPU) / n.TotalCPU
		}
		if n.TotalMemory > 0 {
			used += (n.TotalMemory - n.AvailableMemory) / n.TotalMemory
		}
		mass := j.CPUReq * j.EstimatedDuration // CPU * seconds
		feats = append(feats, feat{
			id: n.Name, ok: true,
			ciCostG: ciNow, waitS: waitS, utilProxy: used, mass: mass, nodeIdx: idx,
		})
	}

	// local min-max scalers
	minMax := func(vals []float64) func(float64) float64 {
		minV, maxV := math.Inf(1), math.Inf(-1)
		for _, v := range vals {
			if v < minV {
				minV = v
			}
			if v > maxV {
				maxV = v
			}
		}
		den := maxV - minV
		if !isFinite(minV) || den < 1e-12 {
			return func(x float64) float64 { return 0 }
		}
		return func(x float64) float64 {
			z := (x - minV) / den
			if z < 0 {
				z = 0
			}
			if z > 1 {
				z = 1
			}
			return z
		}
	}
	waits, utils, masses := []float64{}, []float64{}, []float64{}
	for _, f := range feats {
		if f.ok {
			waits = append(waits, f.waitS)
			utils = append(utils, f.utilProxy)
			masses = append(masses, f.mass)
		}
	}
	waitZ := minMax(waits)
	utilZ := minMax(utils)
	massZ := minMax(masses)

	sc := make(core.Scores, len(feats))
	for _, f := range feats {
		if !f.ok {
			continue
		}
		ci := math.Pow(f.ciCostG, 1.1) // convex penalty
		wz := waitZ(f.waitS) * 0.5
		if wz > 0.5 {
			wz = 0.5
		}
		uz := utilZ(f.utilProxy)

		wEff := p.W.Carbon
		if p.AlphaMass > 0 {
			wEff = wEff * (1.0 + p.AlphaMass*massZ(f.mass))
		}
		score := wEff*ci + p.W.Wait*wz + p.W.Util*uz
		sc[f.id] = score
	}
	if len(sc) == 0 {
		sc[""] = math.Inf(1)
	}
	return sc, nil
}

func isFinite(x float64) bool { return !math.IsNaN(x) && !math.IsInf(x, 0) }



// ----------------- helpers -----------------

// func nodeKey(n core.SimulatedNode) string {
// 	if v, ok := any(n).(interface{ ID() string }); ok {
// 		return v.ID()
// 	}
// 	if v, ok := any(n).(interface{ Name() string }); ok {
// 		return v.Name()
// 	}
// 	return fmt.Sprintf("%p", &n)
// }

// func nextReleaseAfter(n core.SimulatedNode, t time.Time) time.Duration {
// 	if v, ok := any(n).(interface{ NextReleaseAfter(time.Time) time.Duration }); ok {
// 		return v.NextReleaseAfter(t)
// 	}
// 	return 0
// }

// func utilisationOrQueue(n core.SimulatedNode) float64 {
// 	if v, ok := any(n).(interface{ Utilisation() float64 }); ok {
// 		u := v.Utilisation()
// 		if !math.IsNaN(u) && !math.IsInf(u, 0) {
// 			return clamp(u, 0, 1)
// 		}
// 	}
// 	if v, ok := any(n).(interface{ QueueLen() int }); ok {
// 		q := float64(v.QueueLen())
// 		if q < 0 {
// 			q = 0
// 		}
// 		return q
// 	}
// 	return 0
// }

// func clamp(x, lo, hi float64) float64 {
// 	if x < lo {
// 		return lo
// 	}
// 	if x > hi {
// 		return hi
// 	}
// 	return x
// }

// // buildScaler uses cisched.RobustScalingCfg (not core.*)
// func buildScaler(vals []float64, cfg RobustScalingCfg) func(float64) float64 {
// 	clean := make([]float64, 0, len(vals))
// 	for _, v := range vals {
// 		if !math.IsNaN(v) && !math.IsInf(v, 0) {
// 			clean = append(clean, v)
// 		}
// 	}
// 	if len(clean) == 0 {
// 		return func(x float64) float64 { return 0 }
// 	}
// 	sort.Float64s(clean)

// 	if cfg.Enable {
// 		qlo := percentile(clean, cfg.QLow)
// 		qhi := percentile(clean, cfg.QHigh)
// 		den := qhi - qlo
// 		if den < cfg.Eps {
// 			return func(x float64) float64 { return 0 }
// 		}
// 		return func(x float64) float64 { return clamp((x-qlo)/den, 0, 1) }
// 	}

// 	minV := clean[0]
// 	maxV := clean[len(clean)-1]
// 	den := maxV - minV
// 	if den < cfg.Eps {
// 		return func(x float64) float64 { return 0 }
// 	}
// 	return func(x float64) float64 { return clamp((x-minV)/den, 0, 1) }
// }

// func percentile(sorted []float64, q float64) float64 {
// 	if len(sorted) == 0 {
// 		return math.NaN()
// 	}
// 	if q <= 0 {
// 		return sorted[0]
// 	}
// 	if q >= 1 {
// 		return sorted[len(sorted)-1]
// 	}
// 	pos := q * float64(len(sorted)-1)
// 	lo := int(math.Floor(pos))
// 	hi := int(math.Ceil(pos))
// 	if lo == hi {
// 		return sorted[lo]
// 	}
// 	frac := pos - float64(lo)
// 	return sorted[lo] + frac*(sorted[hi]-sorted[lo])
// }
