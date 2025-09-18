package cisched

import "time"

// Weights for the score terms (all inputs are normalised 0..1 before weighting).
type Weights struct {
	Carbon float64 // carbon-impact term
	Wait   float64 // wait proxy term
	Util   float64 // utilisation/queue guard term
}

// Robust scaling config (percentile-based; fallback to min–max if disabled).
type RobustScalingCfg struct {
	Enable bool
	QLow   float64 // e.g. 0.05
	QHigh  float64 // e.g. 0.95
	Eps    float64 // denom guard, e.g. 1e-9
}

type Policy struct {
	W         Weights
	AlphaMass float64       // adaptive carbon scaling by job mass (0=off)
	Lookahead time.Duration // optional look-ahead window (0=off)
	Scale RobustScalingCfg
}

func (p *Policy) Name() string { return "ci_aware" }

// Optional helper you can import in your runner when sweeping.
func RecommendedWeightGrid() []Weights {
	return []Weights{
		{Carbon: 0.5, Wait: 0.0, Util: 0.00},
		{Carbon: 0.8, Wait: 0.2, Util: 0.05},
		{Carbon: 1.1, Wait: 0.2, Util: 0.05},
		{Carbon: 1.4, Wait: 0.2, Util: 0.05},
		{Carbon: 1.4, Wait: 0.4, Util: 0.10},
	}
}
