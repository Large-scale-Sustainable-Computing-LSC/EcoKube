package core

type Site struct {
	ID              string
	PUE             float64 // PUE_s
	K               float64 // k_s (metering calibration)
	CIRegion        string  // region/grid id for forecasts
	CarbonIntensity float64 // fallback carbon intensity gCO₂/kWh
}

type Node struct {
	ID      string
	CPUCap  float64
	MemCap  float64
	Metrics map[string]float64
	Labels  map[string]string
	SiteID  string
	Site    *Site // Injected pointer
}
