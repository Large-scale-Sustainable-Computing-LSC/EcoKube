package core

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"time"
)

type CentralUnit struct {
	Clusters []Cluster
	Strategy SchedulingStrategy
}

type SchedulingDecision struct {
	WorkloadID      string
	StrategyName    string
	SelectedCluster string
	EstimatedCost   float64
	SCI_kWh         float64
	Timestamp       time.Time
	Reasoning       string
}

var allStrategies = []SchedulingStrategy{
	&FCFS{},
	&RoundRobin{},
	&MinMin{},
	&MaxMin{},
	&EnergyAwareStrategy{},
	&CIawareStrategy{},
}

var decisionLog []SchedulingDecision

func (cu CentralUnit) Dispatch(workloads []WorkloadTestbed) {
	n := len(workloads)
	for i := 0; i < n; i++ {
		w := workloads[i]
		selected, reason, err := cu.Strategy.SelectCluster(cu.Clusters, w)
		if err != nil {
			fmt.Printf("[CentralUnit] Failed to schedule %s: %v\n", w.ID, err)
			continue
		}
		selected.SubmitJob(w)
		decision := SchedulingDecision{
			WorkloadID:      w.ID,
			StrategyName:    reflect.TypeOf(cu.Strategy).Name(),
			SelectedCluster: selected.Name(),
			EstimatedCost:   selected.EstimateEnergyCost(w),
			SCI_kWh:         selected.CarbonIntensity(),
			Timestamp:       time.Now(),
			Reasoning:       reason,
		}
		decisionLog = append(decisionLog, decision)
	}
}

func (cu CentralUnit) DispatchAll(workloads []WorkloadTestbed) {
	for _, strategy := range allStrategies {
		fmt.Printf("\n=== Running strategy: %s ===\n", reflect.TypeOf(strategy).Name())
		for _, w := range workloads {
			selected, reason, err := strategy.SelectCluster(cu.Clusters, w)
			if err != nil {
				fmt.Printf("[%-20s] Failed to schedule %s: %v\n", reflect.TypeOf(strategy).Name(), w.ID, err)
				continue
			}
			selected.SubmitJob(w)
			decision := SchedulingDecision{
				WorkloadID:      w.ID,
				StrategyName:    reflect.TypeOf(strategy).Name(),
				SelectedCluster: selected.Name(),
				EstimatedCost:   selected.EstimateEnergyCost(w),
				SCI_kWh:         selected.CarbonIntensity(),
				Timestamp:       time.Now(),
				Reasoning:       reason,
			}
			decisionLog = append(decisionLog, decision)
		}
	}
}

func PrintDecisionTable() {
	fmt.Println("\n================= Scheduling Decision Summary =================")
	fmt.Printf("%-12s %-22s %-16s %-10s %-8s %-10s\n", "Workload", "Strategy", "Cluster", "Cost", "SCI", "Reason")
	for _, d := range decisionLog {
		fmt.Printf("%-12s %-22s %-16s %-10.2f %-8.1f %-10s\n",
			d.WorkloadID, d.StrategyName, d.SelectedCluster, d.EstimatedCost, d.SCI_kWh, d.Reasoning)
	}
}

func LoadClustersFromFile(path string) ([]RemoteCluster, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var clusters []RemoteCluster
	err = json.NewDecoder(file).Decode(&clusters)
	return clusters, err
}
