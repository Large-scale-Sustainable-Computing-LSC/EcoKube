package core

import (
	"fmt"
	"math"
)

type SchedulingStrategy interface {
	SelectCluster([]Cluster, WorkloadTestbed) (Cluster, string, error)
}

type FCFS struct{}

func (s FCFS) SelectCluster(clusters []Cluster, w WorkloadTestbed) (Cluster, string, error) {
	n := len(clusters)
	for i := 0; i < n; i++ {
		c := clusters[i]
		if c.CanAccept(w) {
			return c, "Selected first available cluster", nil
		}
	}
	return nil, "No cluster can accept job", fmt.Errorf("no cluster can accept job")
}

type RoundRobin struct {
	counter int
}

func (s *RoundRobin) SelectCluster(clusters []Cluster, w WorkloadTestbed) (Cluster, string, error) {
	n := len(clusters)
	for i := 0; i < n; i++ {
		c := clusters[(s.counter+i)%n]
		if c.CanAccept(w) {
			s.counter = (s.counter + 1) % n
			return c, fmt.Sprintf("Selected %s using Round Robin", c.Name()), nil
		}
	}
	return nil, "No cluster can accept job", fmt.Errorf("no cluster can accept job")
}

type MinMin struct{}

func (s MinMin) SelectCluster(clusters []Cluster, w WorkloadTestbed) (Cluster, string, error) {
	var best Cluster
	minCost := 1e9
	n := len(clusters)
	for i := 0; i < n; i++ {
		c := clusters[i]
		if c.CanAccept(w) {
			cost := c.EstimateEnergyCost(w)
			if cost < minCost {
				minCost = cost
				best = c
			}
		}
	}
	if best == nil {
		return nil, "No cluster can accept job", fmt.Errorf("no cluster can accept job")
	}
	return best, fmt.Sprintf("Min-Min selected %s with cost %.2f", best.Name(), minCost), nil
}

type MaxMin struct{}

func (s MaxMin) SelectCluster(clusters []Cluster, w WorkloadTestbed) (Cluster, string, error) {
	var best Cluster
	maxCost := -1.0
	n := len(clusters)
	for i := 0; i < n; i++ {
		c := clusters[i]
		if c.CanAccept(w) {
			cost := c.EstimateEnergyCost(w)
			if cost > maxCost {
				maxCost = cost
				best = c
			}
		}
	}
	if best == nil {
		return nil, "No cluster can accept job", fmt.Errorf("no cluster can accept job")
	}
	return best, fmt.Sprintf("Max-Min selected %s with cost %.2f", best.Name(), maxCost), nil
}

type EnergyAwareStrategy struct{}

func (s EnergyAwareStrategy) SelectCluster(clusters []Cluster, w WorkloadTestbed) (Cluster, string, error) {
	var best Cluster
	minCost := 1e9
	n := len(clusters)
	for i := 0; i < n; i++ {
		c := clusters[i]
		if !c.CanAccept(w) {
			continue
		}
		cost := c.EstimateEnergyCost(w)
		if cost < minCost {
			minCost = cost
			best = c
		}
	}
	if best == nil {
		return nil, "No cluster can accept job", fmt.Errorf("no cluster can accept job")
	}
	return best, fmt.Sprintf("Selected %s with lowest energy cost: %.2f", best.Name(), minCost), nil
}

type CIawareStrategy struct{}

func (s CIawareStrategy) SelectCluster(clusters []Cluster, w WorkloadTestbed) (Cluster, string, error) {
	var best Cluster
	lowestScore := 1e9
	reason := ""

	for _, c := range clusters {
		if !c.CanAccept(w) {
			continue
		}

		rc, ok := c.(RemoteCluster)
		if !ok {
			continue
		}

		cpu, err := rc.GetMetricValue("compute_node_cpu_usage")
		if err != nil {
			continue
		}
		utilisation := cpu / 100.0
		if utilisation <= 0 {
			utilisation = 0.01
		}

		ci := rc.CarbonIntensity()               // Default 300.0 gCO₂/kWh
		price := 0.18                            // Example static price per kWh
		power := 150.0                           // Static power draw in Watts
		duration := 10.0                         // Estimated job duration in seconds
		overhead := 5.0                          // Overhead in seconds
		latency := 10.0                          // Placeholder latency

		// Efficiency penalty: inverse of (utilisation * duration^0.3)
		penalty := 1.0 / (math.Pow(utilisation, 0.7) * math.Pow(duration, 0.3))
		energy := (power * duration) / 3600.0    // kWh
		carbon := energy * ci / 1000.0           // gCO₂

		score := 1.0*carbon + 0.5*price + 0.3*overhead + 0.7*penalty + 0.1*latency

		if score < lowestScore {
			best = c
			lowestScore = score
			reason = fmt.Sprintf("CI-aware selected %s (score: %.2f, util: %.2f)", c.Name(), score, utilisation)
		}
	}

	if best == nil {
		return nil, "No cluster can accept job", fmt.Errorf("no cluster can accept job")
	}
	return best, reason, nil
}
