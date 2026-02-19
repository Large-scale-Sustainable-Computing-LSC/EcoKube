package generator

import (
	"encoding/csv"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultStart     = "2025-01-01T08:00:00Z"
	gpuLabelKey      = "gpu"
	resourceClassKey = "resource_class"
)

// WorkloadOptions controls the synthetic mix used for both simulator and Kubernetes runs.
type WorkloadOptions struct {
	Seed                int64
	NumJobs             int
	ArrivalRatePerMin   float64
	ArrivalMode         string // "poisson" or "bursty"
	BurstProbability    float64
	BurstRateMultiplier float64
	BatchSize           int

	LogNormalP25Minutes float64
	LogNormalP75Minutes float64
	ParetoShare         float64
	ParetoAlpha         float64
	ParetoScaleMinutes  float64

	GPUShare float64
}

// DefaultWorkloadOptions returns parameters aligned with the thesis specification.
func DefaultWorkloadOptions() WorkloadOptions {
	return WorkloadOptions{
		Seed:                42,
		NumJobs:             1000,
		ArrivalRatePerMin:   1.0,
		ArrivalMode:         "poisson",
		BurstProbability:    0.10,
		BurstRateMultiplier: 3.0,
		BatchSize:           64,

		LogNormalP25Minutes: 4,
		LogNormalP75Minutes: 20,
		ParetoShare:         0.10,
		ParetoAlpha:         2.2,
		ParetoScaleMinutes:  20,

		GPUShare: 0.15,
	}
}

// GenerateNodes writes a CSV enriched with static GPU labels and CI profiles.
func GenerateNodes(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating dirs for %s: %w", path, err)
	}

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("os.Create(%s): %w", path, err)
	}
	defer f.Close()

	w := csv.NewWriter(f)
	defer w.Flush()

	if err := w.Write([]string{"name", "cpu", "mem", "ci_profile", "site", "peak_power_w", "labels"}); err != nil {
		return fmt.Errorf("writing header: %w", err)
	}

	entries := [][]string{
		{"nl-edge-0", "4", "16", "static:420", "NL", "300", "resource_class=cpu,node_type=edge-cpu"},
		{"nl-edge-1", "4", "16", "static:420", "NL", "300", "resource_class=cpu,node_type=edge-cpu"},
		{"fr-standard-0", "8", "32", "static:70", "FR", "360", "resource_class=cpu,node_type=balanced-cpu"},
		{"fr-standard-1", "8", "32", "static:70", "FR", "360", "resource_class=cpu,node_type=balanced-cpu"},
		{"fr-gpu-0", "16", "64", "static:70", "FR", "470", "gpu=true,resource_class=gpu,node_type=gpu"},
		{"de-memory-0", "12", "96", "static:620", "DE", "460", "resource_class=memory,node_type=memory"},
		{"de-gpu-0", "32", "128", "static:620", "DE", "650", "gpu=true,resource_class=gpu,node_type=gpu"},
		{"de-standard-1", "12", "64", "static:620", "DE", "440", "resource_class=memory,node_type=memory"},
	}

	for _, row := range entries {
		if err := w.Write(row); err != nil {
			return fmt.Errorf("writing node row: %w", err)
		}
	}

	return nil
}

// GenerateWorkloads writes a CSV respecting the provided WorkloadOptions.
func GenerateWorkloads(path string, opts WorkloadOptions) error {
	if opts.NumJobs <= 0 {
		return fmt.Errorf("NumJobs must be positive")
	}
	if opts.ArrivalRatePerMin <= 0 {
		opts.ArrivalRatePerMin = 1.0
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = 1
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating dirs for %s: %w", path, err)
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("os.Create(%s): %w", path, err)
	}
	defer f.Close()

	w := csv.NewWriter(f)
	defer w.Flush()

	header := []string{"id", "submit", "cpu", "mem", "duration", "tag", "preferred_site", "resource_class", "gpu_count"}
	if err := w.Write(header); err != nil {
		return fmt.Errorf("writing header: %w", err)
	}

	rng := rand.New(rand.NewSource(opts.Seed))
	start, err := time.Parse(time.RFC3339, defaultStart)
	if err != nil {
		return fmt.Errorf("parse start time: %w", err)
	}

	mu, sigma := logNormalFromQuantiles(opts.LogNormalP25Minutes, opts.LogNormalP75Minutes)

	current := start
	remaining := opts.NumJobs
	jobIndex := 0
	cpuSites := []string{"FR", "NL", "FR"}
	gpuSites := []string{"FR", "DE", "FR"}
	memorySites := []string{"FR", "NL", "FR"}
	cpuIdx := 0
	gpuIdx := 0
	memoryIdx := 0

	for remaining > 0 {
		wave := minInt(opts.BatchSize, remaining)
		for waveIdx := 0; waveIdx < wave; waveIdx++ {
			jobID := fmt.Sprintf("job-%04d", jobIndex)
			resourceClass, cpu, memGi, gpuCount := sampleResourceClass(rng, opts.GPUShare)
			durationMin := sampleDurationMinutes(rng, mu, sigma, opts.ParetoShare, opts.ParetoAlpha, opts.ParetoScaleMinutes)
			durationSec := int(math.Round(durationMin * 60))
			if durationSec <= 0 {
				durationSec = 60
			}

			tag := resourceClassTag(resourceClass)
			preferredSite := "FR"
			switch resourceClass {
			case "gpu":
				preferredSite = gpuSites[gpuIdx%len(gpuSites)]
				gpuIdx++
			case "memory":
				preferredSite = memorySites[memoryIdx%len(memorySites)]
				memoryIdx++
			default:
				preferredSite = cpuSites[cpuIdx%len(cpuSites)]
				cpuIdx++
			}

			record := []string{
				jobID,
				current.Format(time.RFC3339),
				fmt.Sprintf("%d", cpu),
				fmt.Sprintf("%d", memGi),
				fmt.Sprintf("%d", durationSec),
				tag,
				preferredSite,
				resourceClass,
				fmt.Sprintf("%d", gpuCount),
			}
			if err := w.Write(record); err != nil {
				return fmt.Errorf("writing workload row: %w", err)
			}

			jobIndex++
		}

		remaining -= wave
		if remaining == 0 {
			break
		}
		interval := sampleInterArrival(rng, opts)
		current = current.Add(interval)
	}

	return nil
}

func sampleResourceClass(rng *rand.Rand, gpuShare float64) (class string, cpu int, memGi int, gpuCount int) {
	if gpuShare < 0 {
		gpuShare = 0
	}
	if gpuShare > 0.95 {
		gpuShare = 0.95
	}
	memShare := 0.25
	cpuShare := 1 - gpuShare - memShare
	if cpuShare < 0 {
		cpuShare = 0.5
		memShare = 1 - gpuShare - cpuShare
	}

	u := rng.Float64()
	switch {
	case u < cpuShare:
		class = "cpu"
		cpu = rng.Intn(6) + 2
		memGi = rng.Intn(12) + 8
		gpuCount = 0
	case u < cpuShare+memShare:
		class = "memory"
		cpu = rng.Intn(4) + 2
		memGi = rng.Intn(64-24) + 24
		gpuCount = 0
	default:
		class = "gpu"
		cpu = rng.Intn(12) + 8
		memGi = rng.Intn(96-48) + 48
		gpuCount = 1
	}
	return
}

func resourceClassTag(class string) string {
	switch class {
	case "gpu":
		return "carbon"
	case "memory":
		return "latency"
	default:
		return "throughput"
	}
}

func sampleDurationMinutes(rng *rand.Rand, mu, sigma, paretoShare, paretoAlpha, paretoScale float64) float64 {
	if paretoShare > 0 && rng.Float64() < paretoShare {
		u := rng.Float64()
		return paretoScale / math.Pow(1-u, 1/paretoAlpha)
	}
	return math.Exp(rng.NormFloat64()*sigma + mu)
}

func sampleInterArrival(rng *rand.Rand, opts WorkloadOptions) time.Duration {
	lambdaPerMin := opts.ArrivalRatePerMin
	if lambdaPerMin <= 0 {
		lambdaPerMin = 1.0
	}
	minutes := rng.ExpFloat64() / lambdaPerMin
	if strings.EqualFold(opts.ArrivalMode, "bursty") && rng.Float64() < opts.BurstProbability {
		factor := opts.BurstRateMultiplier
		if factor <= 0 {
			factor = 2
		}
		minutes /= factor
	}
	seconds := minutes * 60.0
	if seconds < 0.1 {
		seconds = 0.1
	}
	return time.Duration(seconds * float64(time.Second))
}

func logNormalFromQuantiles(q25, q75 float64) (mu, sigma float64) {
	if q25 <= 0 || q75 <= 0 {
		return math.Log(8), 1.0 // sensible fallback
	}
	const (
		z25 = -0.6744897501960817
		z75 = 0.6744897501960817
	)
	sigma = math.Log(q75/q25) / (z75 - z25)
	mu = math.Log(q25) - sigma*z25
	return
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
