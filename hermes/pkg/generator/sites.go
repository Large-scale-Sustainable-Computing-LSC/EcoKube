package generator

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// SiteSpec represents a deployment site with calibration parameters.
type SiteSpec struct {
	ID              string
	PUE             float64
	K               float64
	Region          string
	CarbonIntensity float64
}

// DefaultSites returns a trio of heterogeneous sites mirroring the Helm chart.
func DefaultSites() []SiteSpec {
	return []SiteSpec{
		{ID: "A", PUE: 1.18, K: 1.00, Region: "NL", CarbonIntensity: 410},
		{ID: "B", PUE: 1.05, K: 0.95, Region: "ON", CarbonIntensity: 120},
		{ID: "C", PUE: 1.60, K: 1.10, Region: "CA", CarbonIntensity: 520},
	}
}

// GenerateSites writes the default site catalogue to disk.
func GenerateSites(path string) error {
	return WriteSites(path, DefaultSites())
}

// WriteSites writes a CSV of {id,pue,k,region,ci}.
func WriteSites(path string, sites []SiteSpec) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating dirs for %s: %w", path, err)
	}

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("os.Create(%s): %w", path, err)
	}
	defer f.Close()

	w := csv.NewWriter(f)
	defer w.Flush()

	if err := w.Write([]string{"id", "pue", "k", "region", "ci"}); err != nil {
		return fmt.Errorf("writing header: %w", err)
	}

	for _, s := range sites {
		row := []string{
			s.ID,
			strconv.FormatFloat(s.PUE, 'f', 2, 64),
			strconv.FormatFloat(s.K, 'f', 2, 64),
			s.Region,
			strconv.FormatFloat(s.CarbonIntensity, 'f', 0, 64),
		}
		if err := w.Write(row); err != nil {
			return fmt.Errorf("writing site %s: %w", s.ID, err)
		}
	}

	return w.Error()
}
