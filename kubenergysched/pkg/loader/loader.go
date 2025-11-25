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

	header, err := r.Read()
	if err != nil {
		log.Fatalf("read header: %v", err)
	}
	cols := csvIndex(header)

	var nodes []*core.SimulatedNode
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatalf("read record: %v", err)
		}

		name := csvValue(rec, cols, "name")
		if name == "" {
			name = csvValue(rec, cols, "id")
		}
		cpu, _ := strconv.ParseFloat(csvValue(rec, cols, "cpu"), 64)
		mem, _ := strconv.ParseFloat(csvValue(rec, cols, "mem"), 64)
		profile := csvValue(rec, cols, "ci_profile")

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

		if v := csvValue(rec, cols, "site"); v != "" {
			n.SiteID = v
		}
		if v := csvValue(rec, cols, "peak_power_w"); v != "" {
			if n.Metadata == nil {
				n.Metadata = map[string]string{}
			}
			n.Metadata["peak_power_w"] = v
		}
		if labelsRaw := csvValue(rec, cols, "labels"); labelsRaw != "" {
			if n.Labels == nil {
				n.Labels = map[string]string{}
			}
			for _, token := range strings.Split(labelsRaw, ",") {
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
		n.DeviceClass = core.NormaliseClass(csvValue(rec, cols, "device_class"))
		if n.DeviceClass == "" {
			if n.Labels != nil && strings.EqualFold(n.Labels["gpu"], "true") {
				n.DeviceClass = core.ClassGPU
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
	header, err := r.Read()
	if err != nil {
		log.Fatalf("LoadWorkloadsFromCSV: read header: %v", err)
	}
	cols := csvIndex(header)

	var wls []core.Workload
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		} else if err != nil {
			log.Fatalf("LoadWorkloadsFromCSV: read record: %v", err)
		}
		id := csvValue(rec, cols, "id")
		submit, _ := time.Parse(time.RFC3339, csvValue(rec, cols, "submit"))
		cpuF, _ := strconv.ParseFloat(csvValue(rec, cols, "cpu"), 64)
		memF, _ := strconv.ParseFloat(csvValue(rec, cols, "mem"), 64)
		durSec, _ := strconv.Atoi(csvValue(rec, cols, "duration"))
		tag := csvValue(rec, cols, "tag")
		var labels map[string]string
		if preferred := csvValue(rec, cols, "preferred_site"); preferred != "" {
			labels = map[string]string{"preferred_site": preferred}
		}
		resourceClass := csvValue(rec, cols, "resource_class")
		if resourceClass != "" {
			if labels == nil {
				labels = map[string]string{}
			}
			labels["resource_class"] = resourceClass
		}
		if gpuStr := csvValue(rec, cols, "gpu_count"); gpuStr != "" {
			if v, err := strconv.Atoi(gpuStr); err == nil && v > 0 {
				if labels == nil {
					labels = map[string]string{}
				}
				labels["requires_gpu"] = "true"
				labels["gpu_count"] = strconv.Itoa(v)
			}
		}
		classVal := core.NormaliseClass(csvValue(rec, cols, "class"))
		if classVal == "" {
			classVal = core.NormaliseClass(resourceClass)
		}
		if classVal == "" && labels != nil && strings.EqualFold(labels["requires_gpu"], "true") {
			classVal = core.ClassGPU
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
			Class:      classVal,
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

func csvIndex(header []string) map[string]int {
	idx := make(map[string]int, len(header))
	for i, h := range header {
		key := strings.TrimSpace(strings.ToLower(h))
		if key == "" {
			continue
		}
		idx[key] = i
	}
	return idx
}

func csvValue(rec []string, cols map[string]int, key string) string {
	if cols == nil {
		return ""
	}
	idx, ok := cols[strings.TrimSpace(strings.ToLower(key))]
	if !ok || idx < 0 || idx >= len(rec) {
		return ""
	}
	return strings.TrimSpace(rec[idx])
}
