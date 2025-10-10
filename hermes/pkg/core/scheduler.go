package core

import (
	"context"
)

type Scores map[string]float64 // Lower is better.

// ArgMin picks the node ID with minimum score.
func ArgMin(sc Scores) (string, bool) {
	best := ""
	bestV := 0.0
	ok := false
	for id, v := range sc {
		if !ok || v< bestV {
			best, bestV, ok = id, v, true
		}
	}
	return best, ok
}

type Scheduler interface {
	Name() string
	Score(ctx context.Context, job Job, nodes []Node) (Scores, error) // Score each candidate 
	Select(Scores) (string, bool)
}

// func (s *Scheduler) scoreJobOnNode(j Job, n Node, now time.Time) float64 {
//     // 1) Forecasted/observed CI for node's site
//     ci := s.ci.Forecast(n.SiteID, now, j.EstimatedDuration) // gCO2/kWh time series (avg/area)
//     // 2) Energy integral (estimator) and site normalisation
//     eJ := s.energy.EstimateJoules(j, n)                     // ∫ P_j dt (J)
//     ciCost := (eJ/3.6e6) * ci * n.Site.PUE * n.Site.K       // -> grams CO2
//     // 3) Delay/queue proxies
//     wait := s.queue.EstimatedStartDelay(n.SiteID, j, now).Seconds()
//     qlen := s.queue.Length(n.SiteID)
//     // 4) Optional price/repro terms (placeholders if unused)
//     price := s.price.Estimate(n.SiteID, j)
//     repro := s.repro.Penalty(j, n)
//     // 5) Weighted sum (lower is better)
//     return s.W.Carbon*ciCost + s.W.Wait*wait + s.W.Queue*float64(qlen) +
//            s.W.Price*price + s.W.Repro*repro
// }

// func (s *Scheduler) SelectSiteAndNode(j core.Job, now time.Time) (siteID, nodeID string, ok bool) {
//     best := math.Inf(1)
//     for _, site := range s.Sites {
//         if !s.queue.HasCapacity(site.ID, j, now) { continue }
//         cand := s.bestNodeAtSite(j, site, now)    // evaluates scoreJobOnNode
//         if cand.found && cand.score < best {
//             best, siteID, nodeID = cand.score, site.ID, cand.node.ID
//             ok = true
//         }
//     }
//     return
// }