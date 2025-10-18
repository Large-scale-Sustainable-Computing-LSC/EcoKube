package core

import (
	"strconv"
	"time"
)

func estimateEnergyKWh(estimatedDuration float64, labels map[string]string, T time.Duration, k float64) float64 {
	if T <= 0 {
		est := time.Duration(estimatedDuration * float64(time.Second))
		if est <= 0 {
			est = time.Hour
		}
		T = est
	}
	if k == 0 {
		k = 1
	}
	pw := parseNodeFloat(labels, "power_w_mean", 0)
	if pw <= 0 {
		pw = 0.6 * parseNodeFloat(labels, "peak_power_w", 120)
	}
	return k * (pw / 1000.0) * T.Hours()
}

func estimateCarbonKg(energyKWh, pue, ci float64) float64 {
	if pue <= 0 {
		pue = 1
	}
	return energyKWh * pue * (ci / 1000.0)
}

func normaliseCost(energyKWh, carbonKg float64, refs RefScales) (float64, float64) {
	eRef := refs.ERef
	cRef := refs.CRef
	if eRef <= 0 {
		eRef = 1
	}
	if cRef <= 0 {
		cRef = 1
	}
	return energyKWh / eRef, carbonKg / cRef
}

func parseNodeFloat(labels map[string]string, key string, def float64) float64 {
	if labels == nil {
		return def
	}
	v, err := strconv.ParseFloat(labels[key], 64)
	if err != nil {
		return def
	}
	return v
}
