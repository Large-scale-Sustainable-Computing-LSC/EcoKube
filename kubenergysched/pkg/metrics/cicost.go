package metrics

import (
	"math"
	"strings"
	"time"

	"github.com/g-uva/KubEnergySched/kubenergysched/pkg/core"
)

const (
	defaultPeakPowerW = 400.0
	defaultIdleFrac   = 0.15
	defaultCarbonIntG = 400.0
)

// ComputeCICost estimates the grams of CO₂ emitted by running workload w on node n starting at time at.
// It combines a simple power model (idle + CPU utilisation share) with the node/site carbon intensity hints.
func ComputeCICost(n *core.SimulatedNode, w core.Workload, at time.Time) float64 {
	if n == nil {
		return 0
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

	dynamic := math.Max(peak-peak*idleFrac, 0)
	powerW := peak*idleFrac + cpuFrac*dynamic

	dur := w.Duration
	if dur <= 0 {
		dur = time.Second
	}

	energyKWh := (powerW / 1000.0) * dur.Hours()

	if n.Site != nil {
		if n.Site.K > 0 {
			energyKWh *= n.Site.K
		}
	}

	pue := 1.0
	if n.Site != nil && n.Site.PUE > 0 {
		pue = n.Site.PUE
	}

	return energyKWh * pue * (ci / 1000.0)
}

func currentCI(n *core.SimulatedNode, at time.Time) float64 {
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
	default:
		return n.CarbonIntensity
	}
}
