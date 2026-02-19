package topsis

import (
	"context"
	"math"
	"time"

	"github.com/g-uva/EcoKube/policies/internal/candidate"
	"github.com/g-uva/EcoKube/hetsched/pkg/core"
)

// Weights matches the α/β/γ notation for carbon, runtime, and queueing.
type Weights struct {
	Alpha float64
	Beta  float64
	Gamma float64
}

// Policy implements the TOPSIS decision rule over the simulator features.
type Policy struct {
	Weights Weights
	Now     func() time.Time
}

// DefaultWeights returns the calibrated triple used throughout the thesis.
func DefaultWeights() Weights { return Weights{Alpha: 0.58, Beta: 0.21, Gamma: 0.21} }

// Name exposes the scheduler identifier to the simulator harness.
func (p *Policy) Name() string { return "topsis" }

// Score computes the TOPSIS closeness to the ideal solution (minimisation).
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
	ids := make([]string, 0, len(metrics))
	for _, m := range metrics {
		if !m.Feasible {
			continue
		}
		ids = append(ids, m.ID)
		carbonVals = append(carbonVals, m.Carbon)
		runtimeVals = append(runtimeVals, m.Runtime)
		queueVals = append(queueVals, m.Queue)
	}

	scores := core.Scores{}
	if len(ids) == 0 {
		scores[""] = math.Inf(1)
		return scores, nil
	}

	carbonNorm := normaliseVector(carbonVals)
	runtimeNorm := normaliseVector(runtimeVals)
	queueNorm := normaliseVector(queueVals)

	weighted := make([][3]float64, len(ids))
	for i := range ids {
		weighted[i][0] = weights.Alpha * carbonNorm[i]
		weighted[i][1] = weights.Beta * runtimeNorm[i]
		weighted[i][2] = weights.Gamma * queueNorm[i]
	}

	idealBest := [3]float64{math.Inf(1), math.Inf(1), math.Inf(1)}
	idealWorst := [3]float64{math.Inf(-1), math.Inf(-1), math.Inf(-1)}
	for _, vec := range weighted {
		for dim := 0; dim < 3; dim++ {
			if vec[dim] < idealBest[dim] {
				idealBest[dim] = vec[dim]
			}
			if vec[dim] > idealWorst[dim] {
				idealWorst[dim] = vec[dim]
			}
		}
	}

	for i, id := range ids {
		dBest := distance(weighted[i], idealBest)
		dWorst := distance(weighted[i], idealWorst)
		denom := dBest + dWorst
		closeness := 0.0
		if denom > 0 {
			closeness = dWorst / denom
		}
		scores[id] = 1 - closeness
	}

	if len(scores) == 0 {
		scores[""] = math.Inf(1)
	}
	return scores, nil
}

func normaliseVector(vals []float64) []float64 {
	res := make([]float64, len(vals))
	sumSquares := 0.0
	for _, v := range vals {
		sumSquares += v * v
	}
	if sumSquares <= 0 {
		for i := range res {
			res[i] = 0
		}
		return res
	}
	denom := math.Sqrt(sumSquares)
	for i, v := range vals {
		res[i] = v / denom
	}
	return res
}

func distance(vec, target [3]float64) float64 {
	acc := 0.0
	for i := 0; i < 3; i++ {
		diff := vec[i] - target[i]
		acc += diff * diff
	}
	return math.Sqrt(acc)
}
