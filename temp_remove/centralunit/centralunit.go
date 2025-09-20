package main

import (
	"encoding/json"
	"net/http"
	"fmt"
	"log"
	"os"
	"kube-scheduler/pkg/core"
	"time"
	"io"
	"encoding/csv"
	"strings"
	"sort"
	"regexp"
)

var unit core.CentralUnit

func handleWorkloadIngest(w http.ResponseWriter, r *http.Request) {
	var job core.Workload
	if err := json.NewDecoder(r.Body).Decode(&job); err != nil {
		http.Error(w, "invalid job format", http.StatusBadRequest)
		return
	}

	go unit.DispatchAll([]core.WorkloadTestbed{job})
	w.WriteHeader(http.StatusOK)
	fmt.Printf("[CentralUnit] Ingested job: %s\n", job.ID)

	time.Sleep(3 * time.Second)
	core.PrintDecisionTable()
}

func parseLabels(labelStr string) map[string]string {
	result := make(map[string]string)
	re := regexp.MustCompile(`(\w+?)="(.*?)"`)
	matches := re.FindAllStringSubmatch(labelStr, -1)
	for _, match := range matches {
		if len(match) == 3 {
			result[match[1]] = match[2]
		}
	}
	return result
}

func handleMetricsIngest(w http.ResponseWriter, r *http.Request) {
	log.Println("[CentralUnit] /metrics-ingest hit")

	data, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body.", http.StatusInternalServerError)
		return
	}
	defer r.Body.Close()

	lines := strings.Split(string(data), "\n")
	timestamp := time.Now().Format(time.RFC3339)

	metricMap := make(map[string]string)
	for _, line := range lines {
		if strings.HasPrefix(line, "#") || line == "" || !strings.HasPrefix(line, "scaph_") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		key := parts[0]
		val := parts[1]
		metricMap[key] = val
	}

	if len(metricMap) == 0 {
		log.Println("[CentralUnit] No scaph_ metrics found, skipping.")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Write flat row
	f, err := os.OpenFile("/data/metrics_aggregated.csv", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		http.Error(w, "CSV open error", http.StatusInternalServerError)
		return
	}
	defer f.Close()

	writer := csv.NewWriter(f)
	defer writer.Flush()

	// Check if file is empty
	info, _ := f.Stat()
	if info.Size() == 0 {
		header := append([]string{"timestamp"}, keysSorted(metricMap)...)
		writer.Write(header)
	}
	row := []string{timestamp}
	for _, k := range keysSorted(metricMap) {
		row = append(row, metricMap[k])
	}
	writer.Write(row)

	log.Printf("[CentralUnit] Ingested metrics at %s", timestamp)
	w.WriteHeader(http.StatusOK)
}


func handleExportMetricsRange(w http.ResponseWriter, r *http.Request) {
	log.Println("[CentralUnit] /metrics-export-range hit")

	const (
		promURL   = "http://prometheus-operated.eu-central.svc.cluster.local:9090"
		step      = "15s"
		duration  = time.Hour
		outputCSV = "/data/full_scaphandre_metrics.csv"
	)

	now := time.Now()
	start := now.Add(-duration)
	startUnix := start.Unix()
	endUnix := now.Unix()

	log.Printf("Prometheus range query start: %s", start.Format(time.RFC3339))
	log.Printf("Prometheus range query end:   %s", now.Format(time.RFC3339))

	// 1. Get all metric names
	labelURL := fmt.Sprintf("%s/api/v1/label/__name__/values", promURL)
	resp, err := http.Get(labelURL)
	if err != nil {
		log.Printf("Error fetching label values: %v", err)
		http.Error(w, "Failed to fetch labels", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	var labelResp struct {
		Status string   `json:"status"`
		Data   []string `json:"data"`
	}
	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, &labelResp); err != nil {
		log.Printf("Error decoding label JSON: %v", err)
		http.Error(w, "Failed to decode labels", http.StatusInternalServerError)
		return
	}

	var scaphMetrics []string
	for _, m := range labelResp.Data {
		if strings.HasPrefix(m, "scaph_") {
			scaphMetrics = append(scaphMetrics, m)
			log.Printf("Detected metric: %s", m)
		}
	}

	log.Printf("Total scaph_ metrics found: %d", len(scaphMetrics))

	timestamps := map[string]bool{}
	dataByMetric := make(map[string]map[string]string)

	for _, metric := range scaphMetrics {
		queryURL := fmt.Sprintf(
			"%s/api/v1/query_range?query=%s&start=%d&end=%d&step=%s",
			promURL, metric, startUnix, endUnix, step,
		)

		resp, err := http.Get(queryURL)
		if err != nil {
			log.Printf("Query failed for %s: %v", metric, err)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var result struct {
			Status string `json:"status"`
			Data   struct {
				Result []struct {
					Values [][]interface{} `json:"values"`
				} `json:"result"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			log.Printf("Failed to parse %s result: %v", metric, err)
			continue
		}
		if len(result.Data.Result) == 0 {
			log.Printf("Skipping metric (no data): %s", metric)
			continue
		}

		rowMap := make(map[string]string)
		for _, pair := range result.Data.Result[0].Values {
			t, ok1 := pair[0].(float64)
			v, ok2 := pair[1].(string)
			if ok1 && ok2 {
				ts := time.Unix(int64(t), 0).Format(time.RFC3339)
				rowMap[ts] = v
				timestamps[ts] = true
			}
		}
		log.Printf("Metric %s: collected %d points", metric, len(rowMap))
		dataByMetric[metric] = rowMap
	}

	var allTimestamps []string
	for ts := range timestamps {
		allTimestamps = append(allTimestamps, ts)
	}
	sort.Strings(allTimestamps)

	var allMetrics []string
	for m := range dataByMetric {
		allMetrics = append(allMetrics, m)
	}
	sort.Strings(allMetrics)

	rows := [][]string{{"timestamp"}}
	rows[0] = append(rows[0], allMetrics...)

	for _, ts := range allTimestamps {
		row := []string{ts}
		for _, m := range allMetrics {
			val := dataByMetric[m][ts]
			row = append(row, val)
		}
		rows = append(rows, row)
	}

	log.Printf("Writing CSV with %d rows × %d metrics", len(rows)-1, len(allMetrics))

	f, err := os.Create(outputCSV)
	if err != nil {
		log.Printf("CSV write error: %v", err)
		http.Error(w, "Failed to write CSV", http.StatusInternalServerError)
		return
	}
	defer f.Close()

	writer := csv.NewWriter(f)
	defer writer.Flush()
	for _, row := range rows {
		writer.Write(row)
	}

	log.Printf("CSV export complete: %s", outputCSV)
	w.WriteHeader(http.StatusOK)
}



func keysSorted(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}


func main() {
	fmt.Println("Loading clusters from file...")
	clustersRaw, err := core.LoadClustersFromFile("/config/clusters.json")
	if err != nil {
		fmt.Println("Error loading clusters:", err)
		return
	}
	fmt.Printf("Loaded %d clusters from file\n", len(clustersRaw))

	var clusters []core.Cluster
	for _, rc := range clustersRaw {
		clusters = append(clusters, rc)
	}

	unit = core.CentralUnit{
		Clusters: clusters,
		// Strategy: &core.RoundRobin{},
		Strategy: &core.CIawareStrategy{},
	}

	http.HandleFunc("/workload-ingest", handleWorkloadIngest)
	http.HandleFunc("/metrics-ingest", handleMetricsIngest)
	http.HandleFunc("/metrics-export-range", handleExportMetricsRange)

	port := "8080"
	if p := os.Getenv("CENTRAL_PORT"); p != "" {
		port = p
	}
	fmt.Printf("CentralUnit API listening on :%s\n", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}