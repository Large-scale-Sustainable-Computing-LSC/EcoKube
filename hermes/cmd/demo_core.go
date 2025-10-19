package main

import (
    "context"
    "log"
    "net/http"
    "os"
    "time"

    "github.com/g-uva/KubEnergySched/hermes/pkg/engine"
    "github.com/g-uva/KubEnergySched/hermes/pkg/providers"
    "github.com/g-uva/KubEnergySched/hermes/pkg/types"
)

// demo_core shows a minimal end-to-end call to the unified engine.
func main() {
    theta := types.Theta{
        ThetaE: 0.5, ThetaC: 0.5,
        Horizon: 2 * time.Hour, Alpha: 0.95, EgressCapMB: 500,
        ERef: 10, CRef: 5,
    }
    if v := os.Getenv("FORECAST_BASE_URL"); v != "" {
        theta.ForecastBaseURL = v
    }
    deps := engine.Deps{
        CI: nil,
        Theta: theta,
        Refs:  types.RefScales{ERef: theta.ERef, CRef: theta.CRef},
        Weights: types.Weights{E: theta.ThetaE, C: theta.ThetaC},
        Now: func() time.Time { return time.Unix(1_700_000_000, 0) }, // fixed clock for parity
    }
    if theta.ForecastBaseURL != "" {
        deps.CI = &providers.HTTPCIApi{BaseURL: theta.ForecastBaseURL, Client: &http.Client{Timeout: 3 * time.Second}}
    }

    job := types.Job{ID: "j1", CPU: 4, MemoryGB: 8, Tags: map[string]string{}}
    nodes := []types.NodeSnapshot{
        {ID:"n-a", AvailableCPU:8, AvailableGB:32, Site: types.SiteInfo{Name:"siteA", Region:"NL", PUE:1.2, K:1.0, CarbonIntensity:250},
         Labels: map[string]string{"peak_power_w":"150"}},
        {ID:"n-b", AvailableCPU:8, AvailableGB:32, Site: types.SiteInfo{Name:"siteB", Region:"DE", PUE:1.4, K:1.0, CarbonIntensity:380},
         Labels: map[string]string{"peak_power_w":"150"}},
    }

    dec, trace, err := engine.Schedule(context.Background(), job, nodes, deps)
    if err != nil { log.Printf("schedule error: %v", err) }
    log.Printf("DECISION: %#v", dec)
    log.Printf("TRACE:    %#v", trace)
}

