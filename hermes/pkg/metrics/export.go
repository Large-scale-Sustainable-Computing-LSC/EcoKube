package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	decisions = promauto.NewCounterVec(prometheus.CounterOpts{Name: "scheduler_decisions_total"}, []string{"site", "result"})
	energyEst = promauto.NewGaugeVec(prometheus.GaugeOpts{Name: "scheduler_energy_estimate_kwh"}, []string{"job", "site"})
	carbonEst = promauto.NewGaugeVec(prometheus.GaugeOpts{Name: "scheduler_carbon_estimate_kg"}, []string{"job", "site"})
	waitSecs  = promauto.NewHistogram(prometheus.HistogramOpts{Name: "scheduler_wait_seconds"})
	makespan  = promauto.NewHistogram(prometheus.HistogramOpts{Name: "scheduler_makespan_seconds"})
	sloViol   = promauto.NewCounter(prometheus.CounterOpts{Name: "scheduler_slo_violations_total"})
)

func Expose(mux *http.ServeMux) {
	mux.Handle("/metrics", promhttp.Handler())
}

func DecisionsCounter() *prometheus.CounterVec  { return decisions }
func EnergyEstimateGauge() *prometheus.GaugeVec { return energyEst }
func CarbonEstimateGauge() *prometheus.GaugeVec { return carbonEst }
func WaitHistogram() prometheus.Observer        { return waitSecs }
func MakespanHistogram() prometheus.Observer    { return makespan }
func SLOViolationsCounter() prometheus.Counter  { return sloViol }
