package metrics

import (
	"encoding/csv"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/g-uva/EcoKube/hetsched/pkg/core"
)

const (
	defaultPeakPowerW = 400.0
	defaultIdleFrac   = 0.20
	defaultCarbonIntG = 400.0

	ciSmoothWindow = 5
	ciSmoothStep   = 5 * time.Minute

	minCarbonIntensityG = 0.0
	cpuScalingGamma     = 0.8

	defaultWattnetDirEnv = "WATTNET_TRACE_DIR"
)

var (
	wattnetOnce   sync.Once
	wattnetSeries map[string][]wattnetPoint
)

type wattnetPoint struct {
	at time.Time
	ci float64
}

// ComputeEnergyAndCarbon estimates the energy (in kWh) and CO₂ emissions (in kg)
// for running workload w on node n starting at time at.
// The model combines idle/dynamic power with node/site carbon-intensity hints.
func ComputeEnergyAndCarbon(n *core.SimulatedNode, w core.Workload, at time.Time) (energyKWh float64, carbonKg float64) {
	if n == nil {
		return 0, 0
	}
	ci := currentCI(n, at)
	if ci <= 0 {
		if n.Site != nil && n.Site.CarbonIntensity > 0 {
			ci = n.Site.CarbonIntensity
		} else if n.CarbonIntensity > 0 {
			ci = n.CarbonIntensity
		} else {
			ci = defaultCarbonIntG
		}
	}

	peak := defaultPeakPowerW
	if n.Metadata != nil {
		peak = parseFloat(n.Metadata["peak_power_w"], peak)
	}
	if n.Labels != nil {
		if v := n.Labels["peak_power_w"]; v != "" {
			peak = parseFloat(v, peak)
		}
	}

	idleFrac := defaultIdleFrac
	if n.Metadata != nil {
		idleFrac = parseFloat(n.Metadata["idle_power_fraction"], idleFrac)
	}
	if idleFrac < 0 {
		idleFrac = 0
	} else if idleFrac > 1 {
		idleFrac = 1
	}

	cpuFrac := 0.0
	if n.TotalCPU > 0 {
		cpuFrac = w.CPU / n.TotalCPU
	}
	if cpuFrac < 0 {
		cpuFrac = 0
	} else if cpuFrac > 1 {
		cpuFrac = 1
	}
	if cpuFrac > 0 {
		cpuFrac = math.Pow(cpuFrac, cpuScalingGamma)
	}

	dynamic := math.Max(peak-peak*idleFrac, 0)
	powerW := peak*idleFrac + cpuFrac*dynamic

	dur := w.Duration
	if dur <= 0 {
		dur = time.Second
	}

	energyKWh = (powerW / 1000.0) * dur.Hours()

	if n.Site != nil {
		if n.Site.K > 0 {
			energyKWh *= n.Site.K
		}
	}

	energyKWh *= classEfficiencyFactor(w, n)
	energyKWh *= dataMovementFactor(w, n)

	pue := 1.0
	if n.Site != nil && n.Site.PUE > 0 {
		pue = n.Site.PUE
	}

	carbonKg = energyKWh * pue * (ci / 1000.0)

	return energyKWh, carbonKg
}

// ComputeCICost keeps the legacy behaviour of returning grams of CO₂.
func ComputeCICost(n *core.SimulatedNode, w core.Workload, at time.Time) float64 {
	_, carbonKg := ComputeEnergyAndCarbon(n, w, at)
	return carbonKg * 1000.0
}

func currentCI(n *core.SimulatedNode, at time.Time) float64 {
	if n == nil {
		return 0
	}
	if ciSmoothWindow <= 1 {
		return clampCI(rawCarbonIntensity(n, at))
	}
	step := ciSmoothStep
	if step <= 0 {
		step = time.Minute
	}
	sum := 0.0
	count := 0.0
	for i := 0; i < ciSmoothWindow; i++ {
		sampleAt := at.Add(-time.Duration(i) * step)
		sum += rawCarbonIntensity(n, sampleAt)
		count++
	}
	if count == 0 {
		return clampCI(rawCarbonIntensity(n, at))
	}
	return clampCI(sum / count)
}

func rawCarbonIntensity(n *core.SimulatedNode, at time.Time) float64 {
	profile := ""
	if n.Metadata != nil {
		profile = n.Metadata["ci_profile"]
	}
	if profile == "" && n.Labels != nil {
		profile = n.Labels["ci_profile"]
	}
	if profile == "" {
		if n.Site != nil {
			return n.Site.CarbonIntensity
		}
		return n.CarbonIntensity
	}

	parts := strings.Split(profile, ":")
	switch parts[0] {
	case "static":
		if len(parts) >= 2 {
			return parseFloat(parts[1], n.CarbonIntensity)
		}
		return n.CarbonIntensity
	case "sine":
		if len(parts) < 4 {
			return n.CarbonIntensity
		}
		mean := parseFloat(parts[1], n.CarbonIntensity)
		amp := parseFloat(parts[2], 0)
		periodSec := parseFloat(parts[3], 0)
		if periodSec <= 0 {
			return mean
		}
		theta := 2 * math.Pi * float64(at.Unix()%int64(periodSec)) / periodSec
		return mean + amp*math.Sin(theta)
	case "randwalk":
		if len(parts) < 3 {
			return n.CarbonIntensity
		}
		minV := parseFloat(parts[1], n.CarbonIntensity)
		maxV := parseFloat(parts[2], n.CarbonIntensity)
		if maxV <= minV {
			return minV
		}
		// Deterministic pseudo-random walk based on time modulo range.
		span := maxV - minV
		stepSec := 60.0
		if len(parts) >= 4 {
			stepSec = parseFloat(parts[3], stepSec)
			if stepSec <= 0 {
				stepSec = 60
			}
		}
		steps := float64(at.Unix()) / stepSec
		frac := steps - math.Floor(steps)
		return minV + span*frac
	case "wattnet":
		if len(parts) < 3 {
			return n.CarbonIntensity
		}
		country := strings.ToUpper(strings.TrimSpace(parts[1]))
		year := strings.TrimSpace(parts[2])
		if v, ok := lookupWattnetCI(country, year, at.UTC()); ok {
			return v
		}
		return n.CarbonIntensity
	default:
		return n.CarbonIntensity
	}
}

func lookupWattnetCI(country, year string, at time.Time) (float64, bool) {
	loadWattnetOnce()
	if len(wattnetSeries) == 0 {
		return 0, false
	}
	key := strings.ToUpper(strings.TrimSpace(country)) + ":" + strings.TrimSpace(year)
	series, ok := wattnetSeries[key]
	if !ok || len(series) == 0 {
		return 0, false
	}
	if at.IsZero() {
		at = series[0].at
	}
	hour := at.Truncate(time.Hour)
	i := sortSearchByTime(series, hour)
	if i < len(series) && series[i].at.Equal(hour) {
		return series[i].ci, true
	}
	if i > 0 {
		return series[i-1].ci, true
	}
	return series[0].ci, true
}

func sortSearchByTime(series []wattnetPoint, target time.Time) int {
	lo, hi := 0, len(series)
	for lo < hi {
		mid := lo + (hi-lo)/2
		if series[mid].at.Before(target) {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

func loadWattnetOnce() {
	wattnetOnce.Do(func() {
		wattnetSeries = map[string][]wattnetPoint{}
		traceDir := strings.TrimSpace(os.Getenv(defaultWattnetDirEnv))
		if traceDir == "" {
			traceDir = filepath.Join("..", "carbon_intensity_traces_wattnet")
		}
		entries, err := os.ReadDir(traceDir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if !strings.HasSuffix(name, "_hourly.csv") {
				continue
			}
			base := strings.TrimSuffix(name, "_hourly.csv")
			parts := strings.Split(base, "_")
			if len(parts) < 2 {
				continue
			}
			year := parts[len(parts)-1]
			country := strings.Join(parts[:len(parts)-1], "_")
			key := strings.ToUpper(country) + ":" + year
			series := parseWattnetCSV(filepath.Join(traceDir, name))
			if len(series) > 0 {
				wattnetSeries[key] = series
			}
		}
	})
}

func parseWattnetCSV(path string) []wattnetPoint {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	r := csv.NewReader(f)
	recs, err := r.ReadAll()
	if err != nil || len(recs) < 2 {
		return nil
	}
	out := make([]wattnetPoint, 0, len(recs)-1)
	for i := 1; i < len(recs); i++ {
		row := recs[i]
		if len(row) < 2 {
			continue
		}
		t, err := time.Parse("2006-01-02 15:04:05", strings.TrimSpace(row[0]))
		if err != nil {
			continue
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(row[1]), 64)
		if err != nil {
			continue
		}
		out = append(out, wattnetPoint{at: t.UTC(), ci: v})
	}
	return out
}

// EstimateCarbonIntensity returns the forecasted carbon intensity (gCO₂/kWh)
// for node n at time at, matching the model used by ComputeCICost.
func EstimateCarbonIntensity(n *core.SimulatedNode, at time.Time) float64 {
	if n == nil {
		return 0
	}
	return currentCI(n, at)
}

func classEfficiencyFactor(w core.Workload, n *core.SimulatedNode) float64 {
	if n == nil {
		return 1.0
	}
	jobClass := workloadClass(w)
	nodeClass := nodeResourceClass(n)
	if jobClass == "" || nodeClass == "" {
		return 1.0
	}
	if jobClass == nodeClass {
		if jobClass == "gpu" || jobClass == "memory" {
			return 0.90
		}
		return 1.0
	}
	switch jobClass {
	case "memory":
		if nodeClass == "cpu" {
			return 1.45
		}
		if nodeClass == "gpu" {
			return 1.25
		}
	case "cpu":
		if nodeClass == "memory" {
			return 1.10
		}
		if nodeClass == "gpu" {
			return 1.20
		}
	}
	return 1.15
}

func workloadClass(w core.Workload) string {
	if c := strings.ToLower(strings.TrimSpace(w.Class)); c != "" {
		return c
	}
	if w.Labels != nil {
		if c := strings.ToLower(strings.TrimSpace(w.Labels["resource_class"])); c != "" {
			return c
		}
		if strings.EqualFold(w.Labels["requires_gpu"], "true") {
			return "gpu"
		}
	}
	return ""
}

func nodeResourceClass(n *core.SimulatedNode) string {
	if n == nil {
		return ""
	}
	if c := strings.ToLower(strings.TrimSpace(n.DeviceClass)); c != "" {
		return c
	}
	if n.Labels != nil {
		if c := strings.ToLower(strings.TrimSpace(n.Labels["resource_class"])); c != "" {
			return c
		}
		if strings.EqualFold(n.Labels["gpu"], "true") {
			return "gpu"
		}
	}
	return ""
}

func dataMovementFactor(w core.Workload, n *core.SimulatedNode) float64 {
	if n == nil || w.Labels == nil {
		return 1.0
	}
	preferred := strings.TrimSpace(w.Labels["preferred_site"])
	if preferred == "" || n.Site == nil || n.Site.ID == "" || strings.EqualFold(n.Site.ID, preferred) {
		return 1.0
	}
	jobClass := workloadClass(w)
	switch jobClass {
	case "memory":
		return 1.55
	case "gpu":
		return 1.25
	default:
		return 1.15
	}
}

func clampCI(ci float64) float64 {
	if ci < minCarbonIntensityG {
		return minCarbonIntensityG
	}
	return ci
}
