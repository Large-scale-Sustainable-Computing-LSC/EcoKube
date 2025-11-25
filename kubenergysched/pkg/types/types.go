package types

import (
	"time"
)

// Job descriptor (trim to what you actually use)
type Job struct {
	ID                string
	CPU               float64
	MemoryGB          float64
	Deadline          time.Time // optional
	Profile           string    // used by SLO stub if needed
	Tags              map[string]string
	EstimatedDuration float64   // seconds; optional
	SubmitTime        time.Time // creation timestamp
	SlackSeconds      float64   // how long the job can be deferred
	Class             string
}

// Site-level factors (external config)
type SiteInfo struct {
	Name            string
	Region          string  // for CI forecasts
	PUE             float64 // PUE_s
	K               float64 // k_s
	CarbonIntensity float64 // fallback CI in gCO2/kWh
}

// Node snapshot (immutable input for Schedule)
type NodeSnapshot struct {
	ID           string
	Site         SiteInfo
	AvailableCPU float64
	AvailableGB  float64
	Labels       map[string]string
	Metrics      map[string]float64 // e.g. "power_w_mean"
}

// Tunables (Θ surface)
type Theta struct {
	ThetaE, ThetaC  float64
	Lookback        int           // L
	Cadence         time.Duration // Δt
	Horizon         time.Duration // T
	Alpha           float64       // SLO threshold
	EgressCapMB     float64       // ε
	ERef, CRef      float64       // normalisers
	ForecastBaseURL string        // optional HTTP provider base
}

type Weights struct{ E, C float64 }
type RefScales struct{ ERef, CRef float64 }

// Decision + trace for JSONL/metrics
type Decision struct {
	NodeID string
	Scale  int // a_{j,t}; default 1
}

type DecisionTrace struct {
	ResultType   string    `json:"result_type"`
	ResultID     string    `json:"result_id,omitempty"`
	Scheduler    string    `json:"scheduler,omitempty"`
	Source       string    `json:"source,omitempty"`
	RunID        string    `json:"run_id,omitempty"`
	JobID        string    `json:"job_id"`
	Site         string    `json:"site"`
	Node         string    `json:"node"`
	ENorm        float64   `json:"e_norm"`
	CNorm        float64   `json:"c_norm"`
	Cost         float64   `json:"cost"`
	ThetaE       float64   `json:"theta_e"`
	ThetaC       float64   `json:"theta_c"`
	ForecastUsed bool      `json:"forecast_used"`
	Fallback     bool      `json:"fallback"`
	RejectReason string    `json:"reject_reason,omitempty"`
	QueuedAt     time.Time `json:"queued_at,omitempty"`
	StartedAt    time.Time `json:"started_at,omitempty"`
	EndedAt      time.Time `json:"ended_at,omitempty"`
	Scale        int       `json:"scale,omitempty"`
	QueueSeconds float64   `json:"queue_seconds,omitempty"`
	DeferredFor  float64   `json:"deferred_for_seconds,omitempty"`
}
