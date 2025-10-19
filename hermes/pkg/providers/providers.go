package providers

import (
    "context"
    "time"
)

// Forecast CI interface
type CIProvider interface {
    ForecastCI(ctx context.Context, region string, horizon time.Duration) ([]float64, error)
}

// Metrics provider (optional; for live K8s)
type MetricsProvider interface {
    // Placeholder for future extensions (e.g. live power); not needed for the core
}

