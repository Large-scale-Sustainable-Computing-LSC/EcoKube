package generator

import (
	"encoding/csv"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// NodeSpec represents one machine in the cluster.
type NodeSpec struct {
	Name       string
	CPU        int
	Mem        int
	CIProfile  string  // e.g. "static:100", "sine:150:50:3600"
	SiteID     string  // optional site assignment
	PeakPowerW float64 // optional peak power metadata
}

// WorkloadSpec represents one job to submit.
type WorkloadSpec struct {
	ID       string
	Submit   time.Time
	CPU      int
	Mem      int
	Duration time.Duration
	Tag      string
}

// DefaultNodes returns a heterogeneous fleet matching the simulator assumptions.
func DefaultNodes() []NodeSpec {
	nodes := make([]NodeSpec, 0, 11)
	for i := 0; i < 5; i++ {
		nodes = append(nodes, NodeSpec{
			Name:       fmt.Sprintf("small-%d", i),
			CPU:        4,
			Mem:        8,
			CIProfile:  "static:100",
			SiteID:     "A",
			PeakPowerW: 180,
		})
	}
	for i := 0; i < 3; i++ {
		nodes = append(nodes, NodeSpec{
			Name:       fmt.Sprintf("med-%d", i),
			CPU:        8,
			Mem:        16,
			CIProfile:  "static:150",
			SiteID:     "B",
			PeakPowerW: 320,
		})
	}
	for i := 0; i < 2; i++ {
		nodes = append(nodes, NodeSpec{
			Name:       fmt.Sprintf("burst-%d", i),
			CPU:        16,
			Mem:        32,
			CIProfile:  fmt.Sprintf("sine:150:50:%d", 3600),
			SiteID:     "C",
			PeakPowerW: 520,
		})
	}
	nodes = append(nodes, NodeSpec{
		Name:       "gpu-0",
		CPU:        32,
		Mem:        64,
		CIProfile:  "randwalk:100:200:300",
		SiteID:     "C",
		PeakPowerW: 900,
	})
	return nodes
}

// GenerateNodes writes the default node fleet to disk.
func GenerateNodes(path string) error {
	return WriteNodes(path, DefaultNodes())
}

// WriteNodes writes a CSV of {name,cpu,mem,ci_profile,site,peak_power_w}.
func WriteNodes(path string, nodes []NodeSpec) error {
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

	if err := w.Write([]string{"name", "cpu", "mem", "ci_profile", "site", "peak_power_w"}); err != nil {
		return fmt.Errorf("writing header: %w", err)
	}

	for _, n := range nodes {
		row := []string{
			n.Name,
			strconv.Itoa(n.CPU),
			strconv.Itoa(n.Mem),
			n.CIProfile,
			n.SiteID,
		}
		if n.PeakPowerW > 0 {
			row = append(row, strconv.FormatFloat(n.PeakPowerW, 'f', 0, 64))
		} else {
			row = append(row, "")
		}
		if err := w.Write(row); err != nil {
			return fmt.Errorf("writing node %s: %w", n.Name, err)
		}
	}

	return w.Error()
}

// GenerateWorkloads writes a CSV of {id,submit,cpu,mem,duration,tag}.
func GenerateWorkloads(path string, seed int64) error {
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

	if err := w.Write([]string{"id", "submit", "cpu", "mem", "duration", "tag"}); err != nil {
		return fmt.Errorf("writing header: %w", err)
	}

	rand.Seed(seed)
	now := time.Now().UTC()
	for i := 0; i < 1000; i++ {
		typ := rand.Intn(4)
		var cpu, mem, dur int
		var tag string
		switch typ {
		case 0:
			cpu, mem = 1, 1
			dur = rand.Intn(30) + 30
		case 1:
			cpu = rand.Intn(5) + 4
			mem = rand.Intn(13) + 4
			dur = rand.Intn(301) + 300
			tag = "batch"
		case 2:
			cpu, mem = 2, rand.Intn(17)+16
			dur = rand.Intn(61) + 120
			tag = "mem-heavy"
		default:
			cpu = rand.Intn(9) + 8
			mem = rand.Intn(5) + 8
			dur = rand.Intn(201) + 200
			tag = "periodic"
		}

		delta := time.Duration(rand.ExpFloat64()*1e9) * time.Nanosecond
		now = now.Add(delta)
		row := []string{
			fmt.Sprintf("job-%d", i),
			now.Format(time.RFC3339),
			strconv.Itoa(cpu),
			strconv.Itoa(mem),
			strconv.Itoa(dur),
			tag,
		}
		if err := w.Write(row); err != nil {
			return fmt.Errorf("writing workload %s: %w", row[0], err)
		}
	}

	return w.Error()
}

// DurationSecondsFromMicros normalises Google cluster trace durations.
// The trace encodes durations in microseconds. We round up to the nearest
// whole second so the simulator conservatively accounts for execution time.
func DurationSecondsFromMicros(us int64) int {
	if us <= 0 {
		return 0
	}
	return int(math.Ceil(float64(us) / 1_000_000.0))
}
