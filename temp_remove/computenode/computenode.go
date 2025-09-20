package main

import (
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"bytes"
	"encoding/json"
	"io"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

/*
This is a temporary fake lightweight node. Just to test the deployment.
*/

// Fake metrics
var (
	jobCount = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "compute_node_jobs_total",
			Help: "Total number of simulated jobs processed by this compute node.",
		},
	)
	cpuUsage = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "compute_node_cpu_usage",
			Help: "Simulated CPU usage percentage.",
		},
	)
)

func init() {
	prometheus.MustRegister(jobCount)
	prometheus.MustRegister(cpuUsage)
}

type JobRequest struct {
	ID             string  `json:"id"`
	CPURequirement int     `json:"cpu"`
	EnergyPriority float64 `json:"priority"`
}

func handleJobSubmit(w http.ResponseWriter, r *http.Request) {
	var job JobRequest
	if err := json.NewDecoder(r.Body).Decode(&job); err != nil {
		http.Error(w, "Invalid job format", http.StatusBadRequest)
		return
	}
	jobCount.Inc()
	cpu := 30 + rand.Float64()*60
	cpuUsage.Set(cpu)
	fmt.Printf("[ComputeNode] Job %s submitted | CPU: %.2f%%\n", job.ID, cpu)
	w.WriteHeader(http.StatusOK)
}

func handleForwardMetrics(w http.ResponseWriter, r *http.Request) {
	resp, err := http.Get("http://localhost:8080/metrics")
	if err != nil {
		http.Error(w, "Failed to get Scaphandre metrics.", 500)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	httpResponse, httpError := http.Post("http://centralunit.eu-central.svc.cluster.local:8080/metrics-ingest", "text/plain", bytes.NewReader(body))
	if httpError != nil {
		log.Printf("Error sending metrics: %v\n", httpError)
	} else {
		log.Printf("POST response status: %v\n", httpResponse.Status)
	}

	w.WriteHeader(http.StatusOK)
	fmt.Println("[ComputeNode] Submitted metrics to CentralUnit.")
}

func main() {
	http.Handle("/metrics", promhttp.Handler())
	http.HandleFunc("/submit", handleJobSubmit)
	http.HandleFunc("/forward-metrics", handleForwardMetrics)

	portMetrics := "2112"
	if p := os.Getenv("PORT"); p != "" {
		portMetrics = p
	}

	portJob := "9999"
	if p := os.Getenv("JOB_PORT"); p != "" {
		portJob = p
	}

	go func() {
		fmt.Printf("Serving metrics on :%s\n", portMetrics)
		log.Fatal(http.ListenAndServe(":"+portMetrics, nil))
	}()

	fmt.Printf("Serving job submission on :%s\n", portJob)
	log.Fatal(http.ListenAndServe(":"+portJob, nil))
}
