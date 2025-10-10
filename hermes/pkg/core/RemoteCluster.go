package core

import (
	"fmt"
	"encoding/json"
	"bufio"
	"net/http"
	"strconv"
	"strings"
	"bytes"
)

type RemoteCluster struct {
	NameKey string `json:"name"`
	MetricsURL  string `json:"metrics_url"`
	SubmitURL string `json:"submit_url"`
}


func (c RemoteCluster) Name() string { return c.NameKey }

func (c RemoteCluster) CanAccept(w WorkloadTestbed) bool {
	fmt.Printf("[RemoteCluster %s] Sending POST to %s/metrics...\n", c.Name(), c.MetricsURL)
	cpu, err := c.GetMetricValue("compute_node_cpu_usage")
	if err != nil {
		fmt.Printf("[RemoteCluster %s] Error fetching CPU usage: %v\n", c.Name(), err)
		return false
	}
	// Assume node can accept job if CPU is below a threshold
	fmt.Printf("[RemoteCluster %s] Current CPU usage: %.2f%%\n", c.Name(), cpu)
	return cpu < 90.0
}

func (c RemoteCluster) EstimateEnergyCost(w WorkloadTestbed) float64 {
	// Simplified: base cost = CPU × 1.0
	return float64(w.CPURequirement)
}

func (c RemoteCluster) SubmitJob(w WorkloadTestbed) error {
	fmt.Println("RemoteCluster.SubmitJob was called. Workload ID:", w.ID)
	payload := map[string]any{
		"id": w.ID,
		"cpu": w.CPURequirement,
		"priority": w.EnergyPriority,
	}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	fmt.Printf("[RemoteCluster %s] Sending POST to %s/submit...\n", c.Name(), c.SubmitURL)
	resp, err := http.Post(c.SubmitURL+"/submit", "application/json", bytes.NewReader(jsonData))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("submit failed: status %d", resp.StatusCode)
	}

	fmt.Printf("[RemoteCluster %s] Job %s submitted (CPU: %d, Priority: %.2f)\n",
		c.Name(), w.ID, w.CPURequirement, w.EnergyPriority)
	return nil
}


func (c RemoteCluster) CarbonIntensity() float64 {
	// Placeholder — could scrape a real metric
	return 300.0
}

func (c RemoteCluster) GetMetricValue(metricName string) (float64, error) {
	resp, err := http.Get(c.MetricsURL + "/metrics")
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, metricName) {
			parts := strings.Fields(line)
			if len(parts) == 2 {
				return strconv.ParseFloat(parts[1], 64)
			}
		}
	}
	return 0, fmt.Errorf("metric %s not found", metricName)
}