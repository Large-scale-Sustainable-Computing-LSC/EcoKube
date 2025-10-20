package core

import "time"

type DecisionTrace struct {
	ResultType   string    `json:"result_type"`
	ResultID     string    `json:"result_id,omitempty"`
	Scheduler    string    `json:"scheduler,omitempty"`
	Source       string    `json:"source,omitempty"`
	RunID        string    `json:"run_id,omitempty"`
	JobID        string    `json:"job_id"`
	Site         string    `json:"site"`
	Node         string    `json:"node"`
	Etilde       float64   `json:"e_norm"`
	Ctilde       float64   `json:"c_norm"`
	Cost         float64   `json:"cost"`
	ThetaE       float64   `json:"theta_e"`
	ThetaC       float64   `json:"theta_c"`
	ForecastUsed bool      `json:"forecast_used"`
	Fallback     bool      `json:"fallback"`
	RejectReason string    `json:"reject_reason,omitempty"`
	QueuedAt     time.Time `json:"queued_at,omitempty"`
	StartedAt    time.Time `json:"started_at,omitempty"`
	EndedAt      time.Time `json:"ended_at,omitempty"`
}
