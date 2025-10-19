package core

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/g-uva/KubEnergySched/hermes/pkg/forecast"
)

type Scores map[string]float64 // Lower is better.

var ErrNoFeasible = errors.New("core: no feasible node")

// ArgMin picks the node ID with minimum score using a deterministic tie-breaker.
func ArgMin(scores map[string]float64) (id string, ok bool) {
	if len(scores) == 0 {
		return "", false
	}
	best := math.Inf(1)
	var bestID string
	for nodeID, cost := range scores {
		if cost < best || (cost == best && (bestID == "" || nodeID < bestID)) {
			best = cost
			bestID = nodeID
		}
	}
	return bestID, true
}

type Scheduler interface {
	Name() string
	Score(ctx context.Context, job Job, nodes []Node) (Scores, error) // Score each candidate
	Select(Scores) (string, bool)
}

type DecisionTracer interface {
	Record(DecisionTrace) error
}

type TraceablePolicy interface {
	Policy
	Trace(job Job, nodes []SimulatedNode, scores Scores, selected string) *DecisionTrace
}

// SelectSiteAndNode runs the policy scorer and picks the best candidate.
// It returns ErrNoFeasible if no candidate scored or all were infeasible.
func SelectSiteAndNode(ctx context.Context, pol Policy, job Job, nodes []SimulatedNode) (string, Scores, error) {
	scores, err := pol.Score(ctx, job, nodes)
	if err != nil {
		return "", nil, err
	}
	if id, ok := ArgMin(scores); ok {
		return id, scores, nil
	}
	return "", scores, ErrNoFeasible
}

// Weights controls the relative importance of normalised energy and carbon.
type Weights struct {
	E float64
	C float64
}

// RefScales provides reference normalisation factors for energy and carbon.
type RefScales struct {
	ERef float64
	CRef float64
}

// buildCandidateSet filters nodes based on feasibility constraints.
func buildCandidateSet(j Job, nodes []SimulatedNode, alpha, egressCap float64, now time.Time) (cands []SimulatedNode, rejects map[string]string) {
	rejects = map[string]string{}
	for _, n := range nodes {
		if !n.CanAcceptJob(j) {
			rejects[n.ID] = "capacity"
			continue
		}
		if probSLO(j) < alpha {
			rejects[n.ID] = "slo"
			continue
		}
		if estimateEgress(j, &n) > egressCap {
			rejects[n.ID] = "egress"
			continue
		}
		cands = append(cands, n)
	}
	return
}

var (
	probSLOFn        = func(Job) float64 { return 1.0 }
	estimateEgressFn = func(Job, *SimulatedNode) float64 { return 0 }
)

func probSLO(j Job) float64 { return probSLOFn(j) }

func estimateEgress(j Job, n *SimulatedNode) float64 { return estimateEgressFn(j, n) }

// evalCost computes the normalised energy and carbon cost for a candidate node.
func evalCost(j Job, n *SimulatedNode, T time.Duration, refs RefScales, theta Weights, now time.Time, prov forecast.CIProvider) (eT, cT, J float64) {
	var (
		sitePUE     float64
		siteK       float64
		carbonFloat float64
		region      string
	)
	if n.Site != nil {
		sitePUE = n.Site.PUE
		siteK = n.Site.K
		carbonFloat = n.Site.CarbonIntensity
		region = n.Site.CIRegion
	}
	if sitePUE == 0 {
		sitePUE = 1
	}
	if siteK == 0 {
		siteK = 1
	}
	if carbonFloat == 0 {
		carbonFloat = n.CarbonIntensity
	}
	if prov != nil {
		if samples, err := prov.ForecastCI(region, T); err == nil && len(samples) > 0 {
			carbonFloat = samples[0]
		}
	}
	energyKWh := estimateEnergyKWh(j.EstimatedDuration, n.Labels, T, siteK)
	carbonKg := estimateCarbonKg(energyKWh, sitePUE, carbonFloat)
	eT, cT = normaliseCost(energyKWh, carbonKg, refs)
	J = theta.E*eT + theta.C*cT
	return
}
