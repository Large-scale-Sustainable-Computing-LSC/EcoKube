package loader

import (
	"encoding/csv"
	"os"
	"strconv"

	"github.com/g-uva/EcoKube/hetsched/pkg/core"
)

const (
	minPUE = 0.85
	maxPUE = 1.5
	minK   = 0.6
	maxK   = 1.3
)

func LoadSitesFromCSV(path string) map[string]*core.Site {
	f, err := os.Open(path)
	if err != nil {
		panic(err)
	}
	defer f.Close()

	r := csv.NewReader(f)
	rows, _ := r.ReadAll()
	sites := map[string]*core.Site{}

	for i, row := range rows {
		if i == 0 {
			continue
		}
		id := row[0]
		pue, _ := strconv.ParseFloat(row[1], 64)
		pue = clampSite(pue, minPUE, maxPUE)
		k, _ := strconv.ParseFloat(row[2], 64)
		k = clampSite(k, minK, maxK)
		region := row[3]
		sites[id] = &core.Site{ID: id, PUE: pue, K: k, CIRegion: region}
	}
	return sites
}

func AttachSites(nodes []*core.SimulatedNode, sites map[string]*core.Site) {
	for _, n := range nodes {
		if n.Site == nil && n.SiteID != "" {
			if s, ok := sites[n.SiteID]; ok {
				n.Site = s
			}
		}
	}
}

func clampSite(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
