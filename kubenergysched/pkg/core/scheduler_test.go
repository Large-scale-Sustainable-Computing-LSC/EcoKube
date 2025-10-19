package core

import (
	"math"
	"testing"
	"time"
)

func TestBuildCandidateSetFilters(t *testing.T) {
	defer func(prevProb func(Job) float64, prevEgress func(Job, *SimulatedNode) float64) {
		probSLOFn = prevProb
		estimateEgressFn = prevEgress
	}(probSLOFn, estimateEgressFn)

	job := Job{ID: "job", CPUReq: 2, MemReq: 4}
	nodes := []SimulatedNode{
		{ID: "n1", Name: "n1", AvailableCPU: 2, AvailableMemory: 4},
		{ID: "n2", Name: "n2", AvailableCPU: 1, AvailableMemory: 4},
		{ID: "n3", Name: "n3", AvailableCPU: 2, AvailableMemory: 4},
	}

	probSLOFn = func(Job) float64 { return 0.2 }
	cands, rejects := buildCandidateSet(job, nodes, 0.5, math.Inf(1), time.Now())
	if len(cands) != 0 {
		t.Fatalf("expected no candidates due to SLO, got %d", len(cands))
	}
	if reason, ok := rejects["n1"]; !ok || reason != "slo" {
		t.Fatalf("expected n1 rejected for slo, got %q", reason)
	}

	probSLOFn = func(Job) float64 { return 1.0 }
	estimateEgressFn = func(_ Job, n *SimulatedNode) float64 {
		if n.ID == "n3" {
			return 20
		}
		return 0
	}
	cands, rejects = buildCandidateSet(job, nodes, 0.5, 10, time.Now())
	if len(cands) != 1 {
		t.Fatalf("expected 1 candidate after filters, got %d", len(cands))
	}
	if cands[0].ID != "n1" {
		t.Fatalf("expected n1 to be candidate, got %s", cands[0].ID)
	}
	if reason, ok := rejects["n2"]; !ok || reason != "capacity" {
		t.Fatalf("expected n2 rejected for capacity, got %q", reason)
	}
	if reason, ok := rejects["n3"]; !ok || reason != "egress" {
		t.Fatalf("expected n3 rejected for egress, got %q", reason)
	}
}

func TestArgMinDeterministic(t *testing.T) {
	scores := map[string]float64{"b": 1.0, "a": 1.0, "c": 2.0}
	id, ok := ArgMin(scores)
	if !ok {
		t.Fatalf("expected selection")
	}
	if id != "a" {
		t.Fatalf("expected lexicographically smallest id, got %s", id)
	}
}

type stubProvider struct{}

func (stubProvider) ForecastCI(string, time.Duration) ([]float64, error) {
	return []float64{300}, nil
}

func TestEvalCostMonotonic(t *testing.T) {
	job := Job{EstimatedDuration: 3600}
	refs := RefScales{ERef: 1, CRef: 1}
	weights := Weights{E: 0.5, C: 0.5}
	baseNode := &SimulatedNode{Labels: map[string]string{"peak_power_w": "200"}, Site: &Site{PUE: 1.1, K: 1, CIRegion: "x", CarbonIntensity: 200}}

	e1, c1, cost1 := evalCost(job, baseNode, time.Hour, refs, weights, time.Now(), stubProvider{})
	if e1 <= 0 || c1 <= 0 {
		t.Fatalf("expected positive normalised metrics, got %f %f", e1, c1)
	}

	higherPUE := &SimulatedNode{Labels: map[string]string{"peak_power_w": "200"}, Site: &Site{PUE: 1.3, K: 1, CIRegion: "x", CarbonIntensity: 200}}
	_, _, costPUE := evalCost(job, higherPUE, time.Hour, refs, weights, time.Now(), stubProvider{})
	if costPUE <= cost1 {
		t.Fatalf("expected higher cost with larger PUE, got %f <= %f", costPUE, cost1)
	}

	higherCI := &SimulatedNode{Labels: map[string]string{"peak_power_w": "200"}, Site: &Site{PUE: 1.1, K: 1, CIRegion: "x", CarbonIntensity: 400}}
	_, _, costCI := evalCost(job, higherCI, time.Hour, refs, weights, time.Now(), stubProvider{})
	if costCI <= cost1 {
		t.Fatalf("expected higher cost with larger CI, got %f <= %f", costCI, cost1)
	}
}
