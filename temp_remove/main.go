package main

import (
	benchmark "kube-scheduler/benchmark/components"
	core "kube-scheduler/pkg/core"
)

func main() {
	clusters := []core.Cluster{
		core.SimulatedCluster{"eu-central", 16, 1.0, 500, 0, "EU"},
		core.SimulatedCluster{"us-west", 32, 0.8, 350, 0, "US"},
		core.SimulatedCluster{"low-power-node", 8, 0.5, 50, 0, "NL"},
	}

	// workloads := []centralunit.Workload{
	// 	{"job-1", 10, 1.0},
	// 	{"job-2", 20, 0.5},
	// 	{"job-3", 6, 0.9},
	// 	{"job-4", 32, 0.4},
	// 	{"job-5", 4, 1.0},
	// }

    // Generate synthetic workloads
    workloads := benchmark.GenerateSyntheticWorkloads(100)

	strategies := []core.SchedulingStrategy{
		core.FCFS{},
		&core.RoundRobin{},
		core.MinMin{},
		core.MaxMin{},
		core.EnergyAwareStrategy{},
	}

	adapter := benchmark.BenchmarkAdapter{
		Clusters:   clusters,
		Strategies: strategies,
		Workloads:  workloads,
	}

	adapter.RunBenchmark()
	adapter.ExportToCSV()

//     metrics.StartPrometheusServer()
}
