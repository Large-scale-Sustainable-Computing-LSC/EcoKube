package loader

import (
	"encoding/csv"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/g-uva/EcoKube/kubenergysched/pkg/core"
	"gopkg.in/yaml.v3"
)

func LoadNodesFromCSV(path string) []*core.SimulatedNode {
	f, err := os.Open(path)
	if err != nil {
		log.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	r := csv.NewReader(f)

	if _, err := r.Read(); err != nil {
		log.Fatalf("read header: %v", err)
	}

	var nodes []*core.SimulatedNode
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatalf("read record: %v", err)
		}

		name := rec[0]
		cpu, _ := strconv.ParseFloat(rec[1], 64)
		mem, _ := strconv.ParseFloat(rec[2], 64)
		profile := rec[3]

		baseCI := 0.0
		parts := strings.Split(profile, ":")
		switch parts[0] {
		case "static":
			baseCI, _ = strconv.ParseFloat(parts[1], 64)
		case "sine":
			mean, _ := strconv.ParseFloat(parts[1], 64)
			baseCI = mean
		case "randwalk":
			minv, _ := strconv.ParseFloat(parts[1], 64)
			maxv, _ := strconv.ParseFloat(parts[2], 64)
			baseCI = (minv + maxv) / 2.0
		}

		n := core.NewNode(name, cpu, mem, baseCI)
		n.Metadata = map[string]string{"ci_profile": profile}

		if len(rec) >= 5 && rec[4] != "" {
			n.SiteID = rec[4]
		}
		if len(rec) >= 6 && rec[5] != "" {
			if n.Metadata == nil {
				n.Metadata = map[string]string{}
			}
			n.Metadata["peak_power_w"] = rec[5]
		}
		if len(rec) >= 7 && strings.TrimSpace(rec[6]) != "" {
			if n.Labels == nil {
				n.Labels = map[string]string{}
			}
			for _, token := range strings.Split(rec[6], ",") {
				token = strings.TrimSpace(token)
				if token == "" {
					continue
				}
				parts := strings.SplitN(token, "=", 2)
				if len(parts) == 2 {
					n.Labels[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
				} else {
					n.Labels[token] = "true"
				}
			}
		}
		nodes = append(nodes, n)
	}
	return nodes
}

func LoadWorkloadsFromCSV(path string) []core.Workload {
	f, err := os.Open(path)
	if err != nil {
		log.Fatalf("LoadWorkloadsFromCSV: open %s: %v", path, err)
	}
	defer f.Close()

	r := csv.NewReader(f)
	if _, err := r.Read(); err != nil {
		log.Fatalf("LoadWorkloadsFromCSV: read header: %v", err)
	}

	var wls []core.Workload
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		} else if err != nil {
			log.Fatalf("LoadWorkloadsFromCSV: read record: %v", err)
		}
		id := rec[0]
		submit, _ := time.Parse(time.RFC3339, rec[1])
		cpuF, _ := strconv.ParseFloat(rec[2], 64)
		memF, _ := strconv.ParseFloat(rec[3], 64)
		durSec, _ := strconv.Atoi(rec[4])
		tag := ""
		if len(rec) >= 6 {
			tag = rec[5]
		}
		var labels map[string]string
		if len(rec) >= 7 {
			preferred := strings.TrimSpace(rec[6])
			if preferred != "" {
				labels = map[string]string{"preferred_site": preferred}
			}
		}
		resourceClass := ""
		if len(rec) >= 8 {
			resourceClass = strings.TrimSpace(rec[7])
			if resourceClass != "" {
				if labels == nil {
					labels = map[string]string{}
				}
				labels["resource_class"] = resourceClass
			}
		}
		if len(rec) >= 9 && strings.TrimSpace(rec[8]) != "" {
			if v, err := strconv.Atoi(strings.TrimSpace(rec[8])); err == nil && v > 0 {
				if labels == nil {
					labels = map[string]string{}
				}
				labels["requires_gpu"] = "true"
				labels["gpu_count"] = strconv.Itoa(v)
			}
		}
		if tag == "" && resourceClass != "" {
			tag = resourceClass
		}

		wls = append(wls, core.Workload{
			ID:         id,
			SubmitTime: submit,
			Duration:   time.Duration(durSec) * time.Second,
			CPU:        cpuF,
			Memory:     memF,
			Tag:        tag,
			Labels:     labels,
		})
	}
	return wls
}

func LoadTheta(path string) (Theta, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Theta{}, err
	}
	var t Theta
	if err := yaml.Unmarshal(data, &t); err != nil {
		return Theta{}, err
	}
	return t, nil
}

func (t Theta) Weights() core.Weights {
	return core.Weights{E: t.ThetaE, C: t.ThetaC}
}

func (t Theta) Refs() core.RefScales {
	return core.RefScales{ERef: t.ERef, CRef: t.CRef}
}
