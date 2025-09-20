package main

import (
	"encoding/json"
	"net/http"
	"bytes"
	"kube-scheduler/pkg/core"
	benchComponents "kube-scheduler/benchmark/components"
)

func main() {
	clusters, _ := core.LoadClustersFromFile("/config/clusters.json")

	ba := benchComponents.BenchmarkAdapter{
		Clusters:   toClusterInterface(clusters),
		Strategies: []core.SchedulingStrategy{
			&core.RoundRobin{},
			&core.MinMin{},
			&core.MaxMin{},
			&core.EnergyAwareStrategy{},
		},
		Workloads: benchComponents.GenerateSyntheticWorkloads(50),
	}

	ba.RunBenchmark()
	ba.ExportToCSV()
}

func toClusterInterface(rc []core.RemoteCluster) []core.Cluster {
	var result []core.Cluster
	for _, c := range rc {
		result = append(result, c)
	}
	return result
}

func SubmitToCentralUnit(w core.Workload) error {
	url := "http://centralunit.eu-central.svc.cluster.local:8080/workload-ingest"
	body, _ := json.Marshal(w)
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}