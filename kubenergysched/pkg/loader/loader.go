package loader

import (
	"encoding/csv"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/g-uva/KubEnergySched/kubenergysched/pkg/core"
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

		wls = append(wls, core.Workload{
			ID:         id,
			SubmitTime: submit,
			Duration:   time.Duration(durSec) * time.Second,
			CPU:        cpuF,
			Memory:     memF,
			Tag:        tag,
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
