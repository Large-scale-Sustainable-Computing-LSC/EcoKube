package metrics

import "time"

type CFPAggregate struct {
	TotalCIg      float64
	TotalCPUHours float64
	Jobs          int
}

func (a *CFPAggregate) Add(cpu float64, ciG float64, runtime time.Duration) {
	a.TotalCIg += ciG
	a.Jobs++
	a.TotalCPUHours += cpu * runtime.Hours()
}

func (a *CFPAggregate) CFPgPerCPUHour() float64 {
	if a.TotalCPUHours <= 0 {
		return 0
	}
	return a.TotalCIg / a.TotalCPUHours
}
