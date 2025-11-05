package core

import (
	"math"
	"strings"
	"time"
)

type SimulatedNode struct {
	ID              string
	Name            string
	TotalCPU        float64
	TotalMemory     float64
	AvailableCPU    float64
	AvailableMemory float64
	CarbonIntensity float64 // gCO₂/kWh (optional, if we have traces keep it)
	Labels          map[string]string
	Metadata        map[string]string

	Reservations []Reservation
	SiteID       string
	Site         *Site
}

func NewNode(name string, cpu, mem, ci float64) *SimulatedNode {
	return &SimulatedNode{
		Name:            name,
		TotalCPU:        cpu,
		TotalMemory:     mem,
		AvailableCPU:    cpu,
		AvailableMemory: mem,
		CarbonIntensity: ci,
		Labels:          map[string]string{},
		Metadata:        map[string]string{},
		Reservations:    make([]Reservation, 0, 8),
	}
}

func (n *SimulatedNode) CanAccept(w Workload) bool {
	if n.AvailableCPU < w.CPU || n.AvailableMemory < w.Memory {
		return false
	}
	if w.Labels != nil && strings.EqualFold(w.Labels["requires_gpu"], "true") {
		if n.Labels == nil || !strings.EqualFold(n.Labels["gpu"], "true") {
			return false
		}
	}
	return true
}

func (n *SimulatedNode) CanAcceptJob(j Job) bool {
	if n.AvailableCPU < j.CPUReq || n.AvailableMemory < j.MemReq {
		return false
	}
	if j.Labels != nil && strings.EqualFold(j.Labels["requires_gpu"], "true") {
		if n.Labels == nil || !strings.EqualFold(n.Labels["gpu"], "true") {
			return false
		}
	}
	return true
}

func (n *SimulatedNode) Reserve(w Workload, start time.Time) {
	n.AvailableCPU -= w.CPU
	n.AvailableMemory -= w.Memory
	n.Reservations = append(n.Reservations, Reservation{
		End: start.Add(w.Duration),
		CPU: w.CPU,
		Mem: w.Memory,
	})
}

// Release resources for all reservations ending <= t
func (n *SimulatedNode) Release(t time.Time) {
	out := n.Reservations[:0]
	for _, r := range n.Reservations {
		if !r.End.After(t) {
			n.AvailableCPU = math.Min(n.AvailableCPU+r.CPU, n.TotalCPU)
			n.AvailableMemory = math.Min(n.AvailableMemory+r.Mem, n.TotalMemory)
		} else {
			out = append(out, r)
		}
	}
	n.Reservations = out
}

func (n *SimulatedNode) CurrentCINorm(at time.Time) float64 {
	// If we have time-varying CI, normalize here; otherwise use label hints:
	switch n.Labels["ci_profile"] {
	case "low":
		return 0.2
	case "medium":
		return 0.5
	case "high":
		return 0.8
	}
	// Fallback from static CarbonIntensity if set (>0). Example normalization:
	if n.CarbonIntensity > 0 {
		// clamp((ci-50)/650, 0, 1)
		v := (n.CarbonIntensity - 50.0) / 650.0
		if v < 0 {
			v = 0
		}
		if v > 1 {
			v = 1
		}
		return v
	}
	return 0.5
}

func (n *SimulatedNode) NextReleaseAfter(t time.Time) time.Time {
	var earliest time.Time
	for _, r := range n.Reservations {
		if r.End.After(t) && (earliest.IsZero() || r.End.Before(earliest)) {
			earliest = r.End
		}
	}
	return earliest // zero means “none”
}
