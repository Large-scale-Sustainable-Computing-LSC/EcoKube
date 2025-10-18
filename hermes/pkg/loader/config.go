package loader

import "time"

type Theta struct {
	ThetaE          float64       `yaml:"thetaE"`
	ThetaC          float64       `yaml:"thetaC"`
	Lookback        int           `yaml:"lookback"`
	Cadence         time.Duration `yaml:"cadence"`
	Horizon         time.Duration `yaml:"horizon"`
	Alpha           float64       `yaml:"alpha"`
	EgressCapMB     float64       `yaml:"egressCapMB"`
	ERef            float64       `yaml:"eref"`
	CRef            float64       `yaml:"cref"`
	ForecastBaseURL string        `yaml:"forecastBaseURL"`
}
