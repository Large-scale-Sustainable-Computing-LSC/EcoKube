package ecokube

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/g-uva/EcoKube/hetsched/pkg/core"
	"github.com/g-uva/EcoKube/hetsched/pkg/metrics"
)

// Mode selects which heterogeneity-aware scoring strategy to run.
type Mode string

const (
	ModeWeightedSum       Mode = "eco-weighted-sum"
	ModeEpsilonConstraint Mode = "eco-epsilon-constraint"
	ModeGreedyNormalised  Mode = "eco-greedy-normalised"
)

const (
	carbonGuardFraction       = 0.15
	fitBonusBase              = 0.08
	maxFitBonusFraction       = 0.10
	interferencePenaltyFactor = 1.8
	noiseAmplifier            = 1.2
	energyGuardFraction       = 0.15
)

// Config gathers weights and optional thresholds for the composite score.
type Config struct {
	Alpha     float64 // relative impact of carbon emissions
	Beta      float64 // relative impact of runtime projections
	Gamma     float64 // relative impact of queue delay
	Delta     float64 // relative impact of data-movement hints
	FitWeight float64 // relative impact of device-class fit

	// Bounds used by the epsilon-constraint variant (<= 0 disables each bound).
	MaxRuntime      float64
	MaxDataMovement float64

	// Allow tests to override the notion of "now"; nil falls back to time.Now.
	Now func() time.Time
}

// DefaultConfig returns conservative weighting ready to tweak per deployment.
func DefaultConfig() Config {
	return Config{
		Alpha:     0.25,
		Beta:      0.40,
		Gamma:     0.35,
		Delta:     0.0,
		FitWeight: 0.2,
	}
}

// Policy adapts the heterogeneity-aware strategies to the core.Policy contract.
type Policy struct {
	Mode         Mode
	Cfg          Config
	OverrideName string
}

// Name reports the scheduler identifier exposed to the simulator.
func (p *Policy) Name() string {
	if p.OverrideName != "" {
		return p.OverrideName
	}
	if p.Mode == "" {
		return string(ModeWeightedSum)
	}
	return string(p.Mode)
}

// Score implements core.Policy by evaluating every candidate and returning a
// cost map where smaller values represent a better choice.
func (p *Policy) Score(ctx context.Context, job core.Job, nodes []core.SimulatedNode) (core.Scores, error) {
	var now time.Time
	if p.Cfg.Now != nil {
		now = p.Cfg.Now()
	}
	if now.IsZero() {
		now = job.SubmitAt
	}
	if now.IsZero() {
		now = time.Unix(0, 0)
	}

	items, err := computeCandidateMetrics(job, nodes, now)
	if err != nil {
		return nil, err
	}

	switch p.Mode {
	case ModeWeightedSum, "":
		return p.scoreWeighted(items), nil
	case ModeGreedyNormalised:
		return p.scoreWeighted(items), nil
	case ModeEpsilonConstraint:
		return p.scoreEpsilon(items), nil
	default:
		return nil, fmt.Errorf("ecokube: unsupported mode %q", p.Mode)
	}
}

// candidateMetrics stores raw and normalised signals for a node.
type candidateMetrics struct {
	id       string
	feasible bool

	co2     float64
	energy  float64
	runtime float64
	queue   float64
	move    float64
	fit     float64

	co2Hat     float64
	energyHat  float64
	runtimeHat float64
	queueHat   float64
	moveHat    float64
	fitHat     float64
}

func computeCandidateMetrics(job core.Job, nodes []core.SimulatedNode, now time.Time) ([]candidateMetrics, error) {
	if len(nodes) == 0 {
		return nil, errors.New("ecokube: no candidate nodes provided")
	}

	work := workloadFromJob(job)
	items := make([]candidateMetrics, len(nodes))

	co2Vals := make([]float64, 0, len(nodes))
	energyVals := make([]float64, 0, len(nodes))
	rtVals := make([]float64, 0, len(nodes))
	queueVals := make([]float64, 0, len(nodes))
	moveVals := make([]float64, 0, len(nodes))
	fitVals := make([]float64, 0, len(nodes))

	for i, n := range nodes {
		item := candidateMetrics{
			id:       pickNodeID(n),
			feasible: n.CanAcceptJob(job),
		}
		if !item.feasible {
			items[i] = item
			continue
		}

		nCopy := n
		energy, carbon := metrics.ComputeEnergyAndCarbon(&nCopy, work, now)
		item.energy = energy
		item.co2 = carbon * 1000.0
		item.runtime = runtimeSeconds(job)
		item.runtime *= 1 - fitBonus(job, &nCopy)
		if item.runtime < 0 {
			item.runtime = 0
		}
		item.queue = queueSeconds(&nCopy, now)
		item.queue += interferencePenalty(&nCopy, job)
		item.move = dataMovementHint(job, &nCopy)
		item.fit = deviceFitScore(job, nCopy)

		energyVals = append(energyVals, item.energy)
		co2Vals = append(co2Vals, item.co2)
		rtVals = append(rtVals, item.runtime)
		queueVals = append(queueVals, item.queue)
		moveVals = append(moveVals, item.move)
		fitVals = append(fitVals, item.fit)

		items[i] = item
	}

	co2Hats := normaliseMinMax(co2Vals)
	energyHats := normaliseMinMax(energyVals)
	rtHats := normaliseMinMax(rtVals)
	queueHats := normaliseMinMax(queueVals)
	moveHats := normaliseMinMax(moveVals)
	fitHats := normaliseMinMax(fitVals)

	var idx int
	for i := range items {
		if !items[i].feasible {
			continue
		}
		items[i].co2Hat = co2Hats[idx]
		items[i].energyHat = energyHats[idx]
		items[i].runtimeHat = rtHats[idx]
		items[i].queueHat = queueHats[idx]
		items[i].moveHat = moveHats[idx]
		items[i].fitHat = fitHats[idx]
		idx++
	}

	return items, nil
}

func (p *Policy) scoreWeighted(items []candidateMetrics) core.Scores {
	scores := core.Scores{}
	energyCoeff := 1 - (p.Cfg.Alpha + p.Cfg.Beta + p.Cfg.Gamma)
	if energyCoeff < 0 {
		energyCoeff = 0
	}
	for _, it := range items {
		if !it.feasible {
			continue
		}
		fitHat := clamp(it.fitHat, 0, 1)
		fitPenalty := 0.0
		if p.Cfg.FitWeight > 0 {
			fitPenalty = 1 - fitHat
			if fitPenalty < 0 {
				fitPenalty = 0
			}
		}
		score := p.Cfg.Alpha*it.co2Hat +
			energyCoeff*it.energyHat +
			p.Cfg.Beta*it.runtimeHat +
			p.Cfg.Gamma*it.queueHat +
			p.Cfg.Delta*it.moveHat +
			p.Cfg.FitWeight*fitPenalty
		scores[it.id] = score
	}
	if len(scores) == 0 {
		scores[""] = math.Inf(1)
	}
	return scores
}

func clamp(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func (p *Policy) scoreEpsilon(items []candidateMetrics) core.Scores {
	candidates := make([]candidateMetrics, 0, len(items))
	for _, it := range items {
		if !it.feasible {
			continue
		}
		if p.Cfg.MaxRuntime > 0 && it.runtime > p.Cfg.MaxRuntime {
			continue
		}
		if p.Cfg.MaxDataMovement > 0 && it.move > p.Cfg.MaxDataMovement {
			continue
		}
		candidates = append(candidates, it)
	}
	if len(candidates) == 0 {
		for _, it := range items {
			if it.feasible {
				candidates = append(candidates, it)
			}
		}
	}
	if len(candidates) > 0 {
		minCarbon := math.Inf(1)
		minEnergy := math.Inf(1)
		for _, it := range candidates {
			if it.co2 < minCarbon {
				minCarbon = it.co2
			}
			if it.energy < minEnergy {
				minEnergy = it.energy
			}
		}
		filtered := candidates[:0]
		for _, it := range candidates {
			carbonOK := true
			if !math.IsInf(minCarbon, 1) {
				carbonOK = it.co2 <= minCarbon*(1+carbonGuardFraction)
			}
			energyOK := true
			if !math.IsInf(minEnergy, 1) {
				energyOK = it.energy <= minEnergy*(1+energyGuardFraction)
			}
			if carbonOK && energyOK {
				filtered = append(filtered, it)
			}
		}
		if len(filtered) > 0 {
			candidates = filtered
		}
	}
	if len(candidates) == 0 {
		scores := core.Scores{}
		scores[""] = math.Inf(1)
		return scores
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].co2 != candidates[j].co2 {
			return candidates[i].co2 < candidates[j].co2
		}
		if candidates[i].runtime != candidates[j].runtime {
			return candidates[i].runtime < candidates[j].runtime
		}
		return candidates[i].queue < candidates[j].queue
	})

	scores := core.Scores{}
	for rank, it := range candidates {
		scores[it.id] = float64(rank)
	}
	penalty := float64(len(candidates))
	for _, it := range items {
		if !it.feasible {
			continue
		}
		if _, ok := scores[it.id]; !ok {
			scores[it.id] = penalty + 1
		}
	}
	if len(scores) == 0 {
		scores[""] = math.Inf(1)
	}
	return scores
}

func workloadFromJob(j core.Job) core.Workload {
	dur := time.Duration(j.EstimatedDuration * float64(time.Second))
	if dur <= 0 {
		dur = time.Minute
	}
	return core.Workload{
		ID:         j.ID,
		SubmitTime: j.SubmitAt,
		Duration:   dur,
		CPU:        j.CPUReq,
		Memory:     j.MemReq,
		Labels:     j.Labels,
		Class:      j.Class,
	}
}

func pickNodeID(n core.SimulatedNode) string {
	if n.Name != "" {
		return n.Name
	}
	return n.ID
}

func runtimeSeconds(job core.Job) float64 {
	if job.EstimatedDuration > 0 {
		return job.EstimatedDuration
	}
	return 60.0
}

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

func dataMovementHint(job core.Job, n *core.SimulatedNode) float64 {
	if n == nil {
		return 0
	}
	if job.Labels == nil {
		return 0
	}
	target := job.Labels["preferred_site"]
	if target == "" || n.Site == nil {
		return 0
	}
	if n.Site.ID == target {
		return 0
	}
	return 1
}

func fitBonus(job core.Job, n *core.SimulatedNode) float64 {
	if n == nil {
		return 0
	}
	bonus := 0.0
	if job.Labels != nil {
		if strings.EqualFold(job.Labels["requires_gpu"], "true") && hasLabelValue(n.Labels, "gpu", "true", "1") {
			bonus = math.Max(bonus, fitBonusBase)
		}
		if pref := job.Labels["preferred_site"]; pref != "" && n.Site != nil && strings.EqualFold(n.Site.ID, pref) {
			bonus = math.Max(bonus, fitBonusBase*0.6)
		}
		if cls := job.Labels["resource_class"]; cls != "" && hasLabelValue(n.Labels, "resource_class", cls) {
			bonus = math.Max(bonus, fitBonusBase)
		}
		if nodeType := job.Labels["preferred_node_type"]; nodeType != "" && hasLabelValue(n.Labels, "node_type", nodeType) {
			bonus = math.Max(bonus, fitBonusBase)
		}
	}
	return clamp(bonus, 0, maxFitBonusFraction)
}

func interferencePenalty(n *core.SimulatedNode, job core.Job) float64 {
	if n == nil || n.TotalCPU <= 0 || n.TotalMemory <= 0 {
		return 0
	}
	cpuUtil := 1 - clamp(n.AvailableCPU/n.TotalCPU, 0, 1)
	memUtil := 1 - clamp(n.AvailableMemory/n.TotalMemory, 0, 1)
	util := math.Max(cpuUtil, memUtil)
	if util <= 0 {
		return 0
	}
	scale := interferencePenaltyFactor * util * math.Sqrt(math.Max(runtimeSeconds(job), 1))
	if hasLabelValue(n.Labels, "noisy_neighbor", "true") || hasLabelValue(n.Labels, "mixed", "true") {
		scale *= noiseAmplifier
	}
	return scale
}

func hasLabelValue(labels map[string]string, key string, expected ...string) bool {
	if labels == nil {
		return false
	}
	val, ok := labels[key]
	if !ok {
		return false
	}
	if len(expected) == 0 {
		return val != ""
	}
	for _, candidate := range expected {
		if strings.EqualFold(val, candidate) {
			return true
		}
	}
	return false
}

func deviceFitScore(job core.Job, node core.SimulatedNode) float64 {
	jobClass := resolveJobClass(job)
	nodeClass := resolveNodeClass(node)
	if jobClass == "" || nodeClass == "" {
		return 0.5
	}
	if jobClass == nodeClass {
		return 1.0
	}
	if jobClass == core.ClassCPU && (nodeClass == core.ClassGPU || nodeClass == core.ClassMem) {
		return 0.7
	}
	return 0.3
}

func resolveJobClass(job core.Job) string {
	if c := core.NormaliseClass(job.Class); c != "" {
		return c
	}
	if job.Labels != nil {
		if c := core.NormaliseClass(job.Labels["class"]); c != "" {
			return c
		}
		if c := core.NormaliseClass(job.Labels["resource_class"]); c != "" {
			return c
		}
		if strings.EqualFold(job.Labels["requires_gpu"], "true") {
			return core.ClassGPU
		}
	}
	return ""
}

func resolveNodeClass(node core.SimulatedNode) string {
	if c := core.NormaliseClass(node.DeviceClass); c != "" {
		return c
	}
	if node.Labels != nil {
		if c := core.NormaliseClass(node.Labels["device_class"]); c != "" {
			return c
		}
		if c := core.NormaliseClass(node.Labels["resource_class"]); c != "" {
			return c
		}
		if strings.EqualFold(node.Labels["gpu"], "true") {
			return core.ClassGPU
		}
	}
	return ""
}

// normaliseMinMax maps input values to [0,1]. If the range collapses, use 0.5.
func normaliseMinMax(vals []float64) []float64 {
	res := make([]float64, len(vals))
	if len(vals) == 0 {
		return res
	}
	minV := math.Inf(1)
	maxV := math.Inf(-1)
	for _, v := range vals {
		if v < minV {
			minV = v
		}
		if v > maxV {
			maxV = v
		}
	}
	if math.IsInf(minV, 1) || math.IsInf(maxV, -1) {
		for i := range res {
			res[i] = 0.5
		}
		return res
	}
	den := maxV - minV
	if den <= 1e-12 {
		for i := range res {
			res[i] = 0.5
		}
		return res
	}
	for i, v := range vals {
		res[i] = (v - minV) / den
	}
	return res
}
