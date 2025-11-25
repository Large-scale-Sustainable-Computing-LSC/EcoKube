package providers

import (
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "net/http"
    "net/url"
    "strings"
    "time"
)

// HTTPCIApi implements CIProvider over HTTP.
// It expects a JSON array response of float64 values (gCO2/kWh).
type HTTPCIApi struct {
    BaseURL string
    Client  *http.Client
}

// ForecastCI fetches a carbon-intensity forecast for a region and horizon.
// GET <BaseURL>/ci?region=<region>&h=<horizon>
func (h *HTTPCIApi) ForecastCI(ctx context.Context, region string, horizon time.Duration) ([]float64, error) {
    if h == nil {
        return nil, errors.New("HTTPCIApi is nil")
    }
    base := strings.TrimRight(h.BaseURL, "/")
    if base == "" {
        return nil, errors.New("HTTPCIApi.BaseURL is empty")
    }
    // ensure client
    cli := h.Client
    if cli == nil {
        cli = &http.Client{Timeout: 5 * time.Second}
    }

    u := fmt.Sprintf("%s/ci?region=%s&h=%s", base, url.QueryEscape(region), url.QueryEscape(horizon.String()))
    req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
    if err != nil {
        return nil, err
    }
    resp, err := cli.Do(req)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()
    if resp.StatusCode < 200 || resp.StatusCode >= 300 {
        return nil, fmt.Errorf("http ci: status %d", resp.StatusCode)
    }

    var arr []float64
    dec := json.NewDecoder(resp.Body)
    if err := dec.Decode(&arr); err != nil {
        return nil, err
    }
    return arr, nil
}

