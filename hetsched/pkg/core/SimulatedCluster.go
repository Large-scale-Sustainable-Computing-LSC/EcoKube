package core

import "fmt"

type SimulatedCluster struct {
	ClusterName string
	MaxCPU      int
	EnergyBias  float64
	SCI_kWh     float64
	CurrentLoad float64
	Location    string
}

func (c SimulatedCluster) Name() string              { return c.ClusterName }
func (c SimulatedCluster) CanAccept(w WorkloadTestbed) bool { return w.CPURequirement <= c.MaxCPU }
func (c SimulatedCluster) EstimateEnergyCost(w WorkloadTestbed) float64 {
	return float64(w.CPURequirement) * c.EnergyBias
}
func (c SimulatedCluster) SubmitJob(w WorkloadTestbed) error {
	fmt.Printf("[Cluster %s] Job %s submitted (CPU: %d, EnergyBias: %.2f, SCI: %.1f)\n",
		c.ClusterName, w.ID, w.CPURequirement, c.EnergyBias, c.SCI_kWh)
	return nil
}
func (c SimulatedCluster) CarbonIntensity() float64 { return c.SCI_kWh }