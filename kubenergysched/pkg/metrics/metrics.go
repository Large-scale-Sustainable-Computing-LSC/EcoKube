package metrics

import (
	"strconv"
	"time"
)

func EstimateEnergyJ(estimatedDuration float64, labels map[string]string, T time.Duration, k float64) float64 {
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
	pw := parseFloat(labels["power_w_mean"], 0)
	if pw <= 0 {
		pw = 0.6 * parseFloat(labels["peak_power_w"], 120)
	}
	return k * pw * T.Hours() / 1000.0
}

func EstimateCarbonKg(EkWh, PUE, CIg float64) float64 {
	if PUE == 0 {
		PUE = 1
	}
	return EkWh * PUE * (CIg / 1000.0)
}

func Normalise(EkWh, Ckg, eRef, cRef float64) (eT, cT float64) {
	if eRef <= 0 {
		eRef = 1
	}
	if cRef <= 0 {
		cRef = 1
	}
	return EkWh / eRef, Ckg / cRef
}

func parseFloat(s string, def float64) float64 {
	if s == "" {
		return def
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return def
	}
	return v
}
