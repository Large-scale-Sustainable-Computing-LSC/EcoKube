package keids

import (
	"context"
	"math"
	"time"

	"github.com/g-uva/KubEnergySched/kespolicy/internal/candidate"
	"github.com/g-uva/KubEnergySched/kubenergysched/pkg/core"
)

// Weights mirrors the thesis notation (α carbon, β runtime, γ queueing).
type Weights struct {
	Alpha float64
	Beta  float64
	Gamma float64
}

// Policy implements the KEIDS-inspired weighted scheduling rule.
type Policy struct {
	Weights Weights
	Now     func() time.Time
}

// DefaultWeights returns the calibrated α/β/γ triple from the document.
func DefaultWeights() Weights { return Weights{Alpha: 0.45, Beta: 0.35, Gamma: 0.20} }

// Name exposes the scheduler identifier in simulator traces.
func (p *Policy) Name() string { return "keids" }

// Score evaluates each feasible candidate and returns a lower-is-better cost.
func (p *Policy) Score(ctx context.Context, job core.Job, nodes []core.SimulatedNode) (core.Scores, error) {
	now := time.Time{}
	if p.Now != nil {
		now = p.Now()
	}
	if now.IsZero() {
		now = job.SubmitAt
	}
	metrics, err := candidate.Gather(job, nodes, now)
	if err != nil {
		return nil, err
	}

	weights := p.Weights
	if weights == (Weights{}) {
		weights = DefaultWeights()
	}

	carbonVals := make([]float64, 0, len(metrics))
	runtimeVals := make([]float64, 0, len(metrics))
	queueVals := make([]float64, 0, len(metrics))
	for _, m := range metrics {
		if !m.Feasible {
			continue
		}
		carbonVals = append(carbonVals, m.Carbon)
		runtimeVals = append(runtimeVals, m.Runtime)
		queueVals = append(queueVals, m.Queue)
	}

	scores := core.Scores{}
	if len(carbonVals) == 0 {
		scores[""] = math.Inf(1)
		return scores, nil
	}

	carbonHat := candidate.NormaliseMinMax(carbonVals)
	runtimeHat := candidate.NormaliseMinMax(runtimeVals)
	queueHat := candidate.NormaliseMinMax(queueVals)

	idx := 0
	for _, m := range metrics {
		if !m.Feasible {
			continue
		}
		base := weights.Alpha*carbonHat[idx] + weights.Beta*runtimeHat[idx] + weights.Gamma*queueHat[idx]
		// Penalise sustained queueing to approximate interference, staying close to the
		// KEIDS formulation that discourages resource contention.
		interference := 0.0
		if m.Runtime > 0 {
			interference = m.Queue / (m.Runtime + 1)
		}
		score := base + 0.05*weights.Gamma*interference
		scores[m.ID] = score
		idx++
	}

	if len(scores) == 0 {
		scores[""] = math.Inf(1)
	}
	return scores, nil
}
