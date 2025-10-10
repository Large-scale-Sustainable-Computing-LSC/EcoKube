package core

type Cluster interface {
	Name() string
	CanAccept(w WorkloadTestbed) bool
	EstimateEnergyCost(w WorkloadTestbed) float64
	SubmitJob(w WorkloadTestbed) error
	CarbonIntensity() float64
}

