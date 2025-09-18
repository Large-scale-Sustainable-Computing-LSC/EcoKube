// Package: models/cisched/cisched.go
package cisched

import (
	"context"
	"math"
	"time"
	// "fmt"
	// "sort"

	"kube-scheduler/pkg/core"
	"kube-scheduler/pkg/metrics"
)

// Score implements the CI-Aware scorer with robust scaling and a soft util/queue guard.
// NOTE: We adapt Job -> Workload so CanAccept() (which expects Workload) works.
// Score returns a per-node score; lower is better.
func (p *Policy) Score(ctx context.Context, j core.Job, nodes []core.SimulatedNode) (core.Scores, error) {
    // Wrap Job -> Workload once
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
        ciCostG   float64 // ABSOLUTE carbon cost (grams)
        waitS     float64 // simple wait proxy (seconds)
        utilProxy float64 // 0..2 (cpu+mem use)
    }
    feats := make([]feat, 0, len(nodes))
    for _, n := range nodes {
        if !n.CanAccept(w) {
            feats = append(feats, feat{id: n.Name, ok: false})
            continue
        }
        // --- Absolute carbon in grams (includes site PUE*k and CI profile) ---
        ciG := metrics.ComputeCICost(&n, w, now)

        // --- Light queue/wait proxy: time until node has enough free capacity ---
        // If you track this already, plug it here; else 0 means “ready now”.
        waitS := 0.0
        if t := n.NextReleaseAfter(now); !t.IsZero() {
            // crude proxy: how long until *any* release; still helps break ties
            if t.After(now) {
                waitS = t.Sub(now).Seconds()
            }
        }

        // --- Utilisation proxy (cpu+mem) ---
        used := 0.0
        if n.TotalCPU > 0 {
            used += (n.TotalCPU - n.AvailableCPU) / n.TotalCPU
        }
        if n.TotalMemory > 0 {
            used += (n.TotalMemory - n.AvailableMemory) / n.TotalMemory
        }

        feats = append(feats, feat{
            id: n.Name, ok: true,
            ciCostG: ciG, waitS: waitS, utilProxy: used,
        })
    }

    // --- Min–max scale wait/util locally to 0..1 (monotone, no inversions) ---
    minMax := func(vals []float64) (func(float64) float64) {
        minV, maxV := math.Inf(1), math.Inf(-1)
        for _, v := range vals {
            if v < minV { minV = v }
            if v > maxV { maxV = v }
        }
        den := maxV - minV
        if !isFinite(minV) || den < 1e-12 {
            return func(x float64) float64 { return 0.0 }
        }
        return func(x float64) float64 {
            z := (x - minV) / den
            if z < 0 { z = 0 }
            if z > 1 { z = 1 }
            return z
        }
    }
    waits := make([]float64, 0, len(feats))
    utils := make([]float64, 0, len(feats))
    for _, f := range feats {
        if f.ok {
            waits = append(waits, f.waitS)
            utils = append(utils, f.utilProxy)
        }
    }
    waitZ := minMax(waits)
    utilZ := minMax(utils)

    // --- Final score: absolute carbon (grams) + light, scaled wait/util ---
    sc := make(core.Scores, len(feats))
    for _, f := range feats {
        if !f.ok {
            continue
        }
        // // // Optional: convexify carbon a touch to penalise dirty options harder
        // // // ci = ci * ci / (ci + 1e-9) // or math.Pow(ci, 1.1) — leave linear for now
        // ci := f.ciCostG
        // ci = math.Pow(ci, 1.1)  // or ci*ci/(ci+1e-9); gently emphasises dirtier nodes

        // s := p.W.Carbon*ci +
        //      p.W.Wait  *waitZ(f.waitS) +
        //      p.W.Util  *utilZ(f.utilProxy)

        // sc[f.id] = s
        ci := f.ciCostG
        wz := waitZ(f.waitS) * 0.5       // halve influence
        uz := utilZ(f.utilProxy)

        score := p.W.Carbon*ci + p.W.Wait*wz + p.W.Util*uz
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
