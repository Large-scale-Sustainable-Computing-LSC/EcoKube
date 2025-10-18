package forecast

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

type CIProvider interface {
	ForecastCI(region string, horizon time.Duration) ([]float64, error)
}

type HTTPProvider struct {
	BaseURL string
	Client  *http.Client
}

func (p *HTTPProvider) ForecastCI(region string, horizon time.Duration) ([]float64, error) {
	if p == nil {
		return nil, fmt.Errorf("forecast: provider is nil")
	}
	client := p.Client
	if client == nil {
		client = http.DefaultClient
	}
	endpoint := fmt.Sprintf("%s/ci?region=%s&h=%s", p.BaseURL, url.QueryEscape(region), url.QueryEscape(horizon.String()))
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("forecast: unexpected status %s", resp.Status)
	}
	var payload []float64
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	return payload, nil
}
