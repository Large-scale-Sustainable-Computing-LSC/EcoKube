package metrics

import (
	"time"
	"github.com/g-uva/themistack/hermes/pkg/core"
)

type CFPAggregate struct {
	TotalCIg      float64
	TotalCPUHours float64
	Jobs          int
}

func (a *CFPAggregate) Add(w core.Workload, ci_g float64, runtime time.Duration) {
	a.TotalCIg += ci_g
	a.Jobs++
	a.TotalCPUHours += (w.CPU * runtime.Hours())
}

func (a *CFPAggregate) CFPgPerCPUHour() float64 {
	if a.TotalCPUHours <= 0 { return 0 }
	return a.TotalCIg / a.TotalCPUHours
}
