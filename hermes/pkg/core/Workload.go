package core

import (
	"time"
)

// Workload represents a job to schedule
type Workload struct {
	ID         string
	SubmitTime time.Time
	Duration   time.Duration
	CPU        float64
	Memory     float64
	Tag		 string
	Labels	 map[string]string
}

type WorkloadTestbed struct {
	ID             string
	CPURequirement int
	EnergyPriority float64 // 0.0 to 1.0, higher = more energy aware
}
