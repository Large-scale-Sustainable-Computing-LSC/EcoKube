package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/g-uva/KubEnergySched/kespolicy/carbonscaler"
	"github.com/g-uva/KubEnergySched/kespolicy/hetpolicy"
	"github.com/g-uva/KubEnergySched/kespolicy/k8sched"
	"github.com/g-uva/KubEnergySched/kespolicy/keids"
	"github.com/g-uva/KubEnergySched/kespolicy/topsis"
	"github.com/g-uva/KubEnergySched/kubenergysched/pkg/core"
	"github.com/g-uva/KubEnergySched/kubenergysched/pkg/loader"
	"github.com/g-uva/KubEnergySched/kubenergysched/pkg/metrics"
)

// cross-product: (policy × ci_weight × batch_size)
type SummaryRow struct {
	Policy            string  `json:"policy"`
	CIWeight          float64 `json:"ci_weight"`
	BatchSize         int     `json:"batch_size"`
	TotalCICostG      float64 `json:"total_ci_cost_g"`
	AvgCIPerJobG      float64 `json:"avg_ci_per_job_g"`
	CFPgPerCPUHour    float64 `json:"cfp_g_per_cpu_hour"`
	AvgWaitS          float64 `json:"avg_wait_s"`
	MakespanS         float64 `json:"makespan_s"`
	ElapsedMs         float64 `json:"elapsed_ms"`
	NumJobs           int     `json:"num_jobs"`
	Alpha             float64 `json:"alpha"`
	Beta              float64 `json:"beta"`
	Gamma             float64 `json:"gamma"`
	AlphaMass         float64 `json:"alpha_mass"`
	LookaheadMin      int     `json:"lookahead_min"`
	DurationScale     float64 `json:"duration_scale"`
	DurationOverrides string  `json:"duration_overrides"`
}

type hetWeightSet struct {
	Alpha float64
	Beta  float64
	Gamma float64
	Label string
}

func main() {
	// ---- CLI flags ----
	var nodesCSV, wlCSV, sitesCSV, outDir string
	var ciWeightsFlag, batchSizesFlag string
	var durationsFlag string
	var durScale float64
	var alphaMass float64
	var lookaheadMin int
	var tracePath string

	flag.StringVar(&nodesCSV, "nodes-csv", "config/nodes.csv", "path to nodes CSV")
	flag.StringVar(&wlCSV, "wl-csv", "config/workloads.csv", "path to workloads CSV")
	flag.StringVar(&sitesCSV, "sites-csv", "", "path to sites CSV (defaults to nodes directory/sites.csv)")
	flag.StringVar(&outDir, "outdir", "", "output directory for per-run CSVs and summary (default kubenergysched/results)")
	flag.StringVar(&ciWeightsFlag, "ci-weights", "0.4", "comma-separated base CI weights to sweep")
	flag.StringVar(&batchSizesFlag, "batch-sizes", "64", "comma-separated batch sizes to sweep")
	flag.StringVar(&durationsFlag, "durations", "", "override job durations (seconds) as comma-separated list; assigned round-robin")
	flag.Float64Var(&durScale, "dur-scale", 1.0, "multiply all job durations by this factor")
	flag.Float64Var(&alphaMass, "alpha-mass", 1.0, "adaptive carbon weight multiplier for big jobs (0=off)")
	flag.IntVar(&lookaheadMin, "lookahead-min", 0, "look-ahead window in minutes (0=off)")
	flag.StringVar(&tracePath, "trace-jsonl", "", "append JSON decision traces to this file (use 'auto' for outdir/decisions.jsonl)")
	var hetModesFlag string
	flag.StringVar(&hetModesFlag, "het-modes", "het-weighted-sum", "comma-separated hetero policy modes (weighted-sum, epsilon-constraint, greedy-normalised)")
	var hetWeightsFlag string
	flag.StringVar(&hetWeightsFlag, "het-weights", "", "comma-separated alpha:beta:gamma sets for hetpolicy (e.g. '0.6:0.3:0.1'); leave empty or 'auto' to calibrate per CI weight")
	flag.Parse()

	if outDir == "" {
		outDir = "results"
	}
	must(os.MkdirAll(outDir, 0o755))
	cleanResults(outDir)

	if sitesCSV == "" {
		sitesCSV = filepath.Join(filepath.Dir(nodesCSV), "sites.csv")
	}

	if tracePath == "auto" {
		tracePath = filepath.Join(outDir, "decisions.jsonl")
	}

	var (
		tracer core.DecisionTracer
	)
	if tracePath != "" {
		must(os.MkdirAll(filepath.Dir(tracePath), 0o755))
		tw, err := core.NewJSONTraceWriter(tracePath)
		must(err)
		tracer = tw
		defer func() { _ = tw.Close() }()
		fmt.Printf("Tracing decisions to %s\n", tracePath)
	}

	ciWeights := parseFloatSlice(ciWeightsFlag)
	batchSizes := parseIntSlice(batchSizesFlag)
	overrideDurations := parseDurationSliceSeconds(durationsFlag)
	hetModes := parseHetModes(hetModesFlag)
	if len(hetModes) > 1 {
		log.Printf("het-modes: limiting to first mode %q for deterministic comparison", hetModes[0])
		hetModes = hetModes[:1]
	}
	hetWeightSets := parseHetWeightSets(hetWeightsFlag)

	// ---- Load workloads once and apply duration knobs ----
	workloads := loader.LoadWorkloadsFromCSV(wlCSV)
	if durScale != 1.0 {
		for i := range workloads {
			workloads[i].Duration = time.Duration(float64(workloads[i].Duration) * durScale)
		}
	}
	if len(overrideDurations) > 0 {
		for i := range workloads {
			workloads[i].Duration = overrideDurations[i%len(overrideDurations)]
		}
	}

	// keep a copy to rebuild map later per run
	templateWl := make([]core.Workload, len(workloads))
	copy(templateWl, workloads)

	var allSummaries []SummaryRow

	for _, ciW := range ciWeights {
		for _, bs := range batchSizes {
			specs := []struct {
				name string
				run  func([]core.Workload) ([]core.LogEntry, float64)
			}{
				{
					name: "k8s",
					run: func(w []core.Workload) ([]core.LogEntry, float64) {
						const policyID = "k8s"
						nodes := loader.LoadNodesFromCSV(nodesCSV)
						sites := loader.LoadSitesFromCSV(sitesCSV)
						loader.AttachSites(nodes, sites)

						pol := &k8sched.Policy{}
						sim := &core.BaseSim{}
						sim.Init(nodes, pol)
						if tracer != nil {
							sim.SetTracer(tracer)
						}
						sim.SetScheduleBatchSize(bs)
						sim.CICalc = func(n *core.SimulatedNode, w core.Workload, at time.Time) float64 {
							return metrics.ComputeCICost(n, w, at)
						}

						workloadByID := map[string]core.Workload{}
						for _, j := range w {
							workloadByID[j.ID] = j
							sim.AddWorkload(j)
						}

						start := time.Now()
						sim.Run()
						elapsed := float64(time.Since(start).Milliseconds())

						logs := sim.Logs()
						writePerJobCSV(outDir, policyID, ciW, bs, logs)

						s := summariseRun(policyID, ciW, bs, logs, workloadByID)
						s.ElapsedMs = elapsed
						s.AlphaMass = alphaMass
						s.LookaheadMin = lookaheadMin
						s.DurationScale = durScale
						s.DurationOverrides = durationsFlag
						allSummaries = append(allSummaries, s)
						return logs, elapsed
					},
				},
				{
					name: "keids",
					run: func(w []core.Workload) ([]core.LogEntry, float64) {
						const policyID = "keids"
						nodes := loader.LoadNodesFromCSV(nodesCSV)
						sites := loader.LoadSitesFromCSV(sitesCSV)
						loader.AttachSites(nodes, sites)

						pol := &keids.Policy{Weights: keids.DefaultWeights()}
						sim := &core.BaseSim{}
						sim.Init(nodes, pol)
						if tracer != nil {
							sim.SetTracer(tracer)
						}
						sim.SetScheduleBatchSize(bs)
						sim.CICalc = func(n *core.SimulatedNode, w core.Workload, at time.Time) float64 {
							return metrics.ComputeCICost(n, w, at)
						}

						workloadByID := map[string]core.Workload{}
						for _, j := range w {
							workloadByID[j.ID] = j
							sim.AddWorkload(j)
						}

						start := time.Now()
						sim.Run()
						elapsed := float64(time.Since(start).Milliseconds())

						logs := sim.Logs()
						writePerJobCSV(outDir, policyID, ciW, bs, logs)

						s := summariseRun(policyID, ciW, bs, logs, workloadByID)
						s.ElapsedMs = elapsed
						s.Alpha = pol.Weights.Alpha
						s.Beta = pol.Weights.Beta
						s.Gamma = pol.Weights.Gamma
						s.AlphaMass = alphaMass
						s.LookaheadMin = lookaheadMin
						s.DurationScale = durScale
						s.DurationOverrides = durationsFlag
						allSummaries = append(allSummaries, s)
						return logs, elapsed
					},
				},
				{
					name: "topsis",
					run: func(w []core.Workload) ([]core.LogEntry, float64) {
						const policyID = "topsis"
						nodes := loader.LoadNodesFromCSV(nodesCSV)
						sites := loader.LoadSitesFromCSV(sitesCSV)
						loader.AttachSites(nodes, sites)

						pol := &topsis.Policy{Weights: topsis.DefaultWeights()}
						sim := &core.BaseSim{}
						sim.Init(nodes, pol)
						if tracer != nil {
							sim.SetTracer(tracer)
						}
						sim.SetScheduleBatchSize(bs)
						sim.CICalc = func(n *core.SimulatedNode, w core.Workload, at time.Time) float64 {
							return metrics.ComputeCICost(n, w, at)
						}

						workloadByID := map[string]core.Workload{}
						for _, j := range w {
							workloadByID[j.ID] = j
							sim.AddWorkload(j)
						}

						start := time.Now()
						sim.Run()
						elapsed := float64(time.Since(start).Milliseconds())

						logs := sim.Logs()
						writePerJobCSV(outDir, policyID, ciW, bs, logs)

						s := summariseRun(policyID, ciW, bs, logs, workloadByID)
						s.ElapsedMs = elapsed
						s.Alpha = pol.Weights.Alpha
						s.Beta = pol.Weights.Beta
						s.Gamma = pol.Weights.Gamma
						s.AlphaMass = alphaMass
						s.LookaheadMin = lookaheadMin
						s.DurationScale = durScale
						s.DurationOverrides = durationsFlag
						allSummaries = append(allSummaries, s)
						return logs, elapsed
					},
				},
				{
					name: "carbonscaler",
					run: func(w []core.Workload) ([]core.LogEntry, float64) {
						const policyID = "carbonscaler"
						lambda := carbonScalerLambda(ciW)
						nodes := loader.LoadNodesFromCSV(nodesCSV)
						sites := loader.LoadSitesFromCSV(sitesCSV)
						loader.AttachSites(nodes, sites)

						pol := &carbonscaler.Policy{Cfg: carbonscaler.Config{Lambda: lambda}}
						sim := &core.BaseSim{}
						sim.Init(nodes, pol)
						if tracer != nil {
							sim.SetTracer(tracer)
						}
						sim.SetScheduleBatchSize(bs)
						sim.CICalc = func(n *core.SimulatedNode, w core.Workload, at time.Time) float64 {
							return metrics.ComputeCICost(n, w, at)
						}

						workloadByID := map[string]core.Workload{}
						for _, j := range w {
							workloadByID[j.ID] = j
							sim.AddWorkload(j)
						}

						start := time.Now()
						sim.Run()
						elapsed := float64(time.Since(start).Milliseconds())

						logs := sim.Logs()
						writePerJobCSV(outDir, policyID, ciW, bs, logs)

						s := summariseRun(policyID, ciW, bs, logs, workloadByID)
						s.ElapsedMs = elapsed
						s.AlphaMass = alphaMass
						s.LookaheadMin = lookaheadMin
						s.DurationScale = durScale
						s.DurationOverrides = durationsFlag
						allSummaries = append(allSummaries, s)
						return logs, elapsed
					},
				},
			}

			for _, mode := range hetModes {
				mode := mode
				weights := hetWeightSets
				if len(weights) == 0 {
					alpha, beta, gamma := calibrateHetWeights(ciW)
					weights = []hetWeightSet{{
						Alpha: alpha,
						Beta:  beta,
						Gamma: gamma,
						Label: formatWeightLabel(alpha, beta, gamma),
					}}
				}
				for _, weightCfg := range weights {
					weightCfg := weightCfg
					policyID := "hetpolicy"
					if len(hetWeightSets) > 0 {
						policyID = formatHetPolicyID(mode, weightCfg.Label)
					}
					specs = append(specs, struct {
						name string
						run  func([]core.Workload) ([]core.LogEntry, float64)
					}{
						name: policyID,
						run: func(w []core.Workload) ([]core.LogEntry, float64) {
							nodes := loader.LoadNodesFromCSV(nodesCSV)
							sites := loader.LoadSitesFromCSV(sitesCSV)
							loader.AttachSites(nodes, sites)

							sim := &core.BaseSim{}
							cfg := hetpolicy.DefaultConfig()
							cfg.Delta = 0.0
							cfg.Alpha = weightCfg.Alpha
							cfg.Beta = weightCfg.Beta
							cfg.Gamma = weightCfg.Gamma
							pol := &hetpolicy.Policy{
								Mode:         mode,
								Cfg:          cfg,
								OverrideName: policyID,
							}
							sim.Init(nodes, pol)
							if tracer != nil {
								sim.SetTracer(tracer)
							}
							sim.SetScheduleBatchSize(bs)
							sim.CICalc = func(n *core.SimulatedNode, w core.Workload, at time.Time) float64 {
								return metrics.ComputeCICost(n, w, at)
							}

							workloadByID := map[string]core.Workload{}
							for _, j := range w {
								workloadByID[j.ID] = j
								sim.AddWorkload(j)
							}

							start := time.Now()
							sim.Run()
							elapsed := float64(time.Since(start).Milliseconds())

							logs := sim.Logs()
							writePerJobCSV(outDir, policyID, ciW, bs, logs)

							s := summariseRun(policyID, ciW, bs, logs, workloadByID)
							s.ElapsedMs = elapsed
							s.Alpha = weightCfg.Alpha
							s.Beta = weightCfg.Beta
							s.Gamma = weightCfg.Gamma
							s.AlphaMass = alphaMass
							s.LookaheadMin = lookaheadMin
							s.DurationScale = durScale
							s.DurationOverrides = durationsFlag
							allSummaries = append(allSummaries, s)
							return logs, elapsed
						},
					})
				}
			}

			// run all 3 schedulers for this (ciW, bs)
			for _, spec := range specs {
				// use a fresh copy of workloads per spec
				wcopy := make([]core.Workload, len(templateWl))
				copy(wcopy, templateWl)
				_, _ = spec.run(wcopy)
			}
		}
	}

	// ---- write combined summary CSV + JSON ----
	writeSummary(outDir, allSummaries)
	fmt.Printf("Wrote %d summary rows to %s\n", len(allSummaries), filepath.Join(outDir, "summary.csv"))
}

// ---- Helpers ----

func parseFloatSlice(s string) []float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]float64, 0, len(parts))
	for _, p := range parts {
		if v, err := strconv.ParseFloat(strings.TrimSpace(p), 64); err == nil {
			out = append(out, v)
		}
	}
	return out
}

func parseIntSlice(s string) []int {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		if v, err := strconv.Atoi(strings.TrimSpace(p)); err == nil {
			out = append(out, v)
		}
	}
	return out
}

func parseDurationSliceSeconds(s string) []time.Duration {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	fs := parseFloatSlice(s)
	out := make([]time.Duration, len(fs))
	for i, v := range fs {
		out[i] = time.Duration(v * float64(time.Second))
	}
	return out
}

func parseStringSlice(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if token := strings.TrimSpace(p); token != "" {
			out = append(out, token)
		}
	}
	return out
}

func parseHetModes(raw string) []hetpolicy.Mode {
	tokens := parseStringSlice(raw)
	if len(tokens) == 0 {
		return []hetpolicy.Mode{hetpolicy.ModeWeightedSum}
	}
	seen := map[hetpolicy.Mode]struct{}{}
	modes := make([]hetpolicy.Mode, 0, len(tokens))
	for _, token := range tokens {
		if mode, ok := resolveHetMode(token); ok {
			if _, exists := seen[mode]; !exists {
				seen[mode] = struct{}{}
				modes = append(modes, mode)
			}
		} else {
			log.Printf("unknown het-mode %q ignored", token)
		}
	}
	if len(modes) == 0 {
		return []hetpolicy.Mode{hetpolicy.ModeWeightedSum}
	}
	return modes
}

func parseHetWeightSets(raw string) []hetWeightSet {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.EqualFold(raw, "auto") {
		return nil
	}
	tokens := strings.Split(raw, ",")
	sets := make([]hetWeightSet, 0, len(tokens))
	for _, token := range tokens {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		parts := strings.Split(token, ":")
		if len(parts) != 3 {
			log.Printf("het-weights: expected alpha:beta:gamma, got %q", token)
			continue
		}
		vals := make([]float64, 0, 3)
		for _, part := range parts {
			v, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
			if err != nil {
				log.Printf("het-weights: ignoring %q (%v)", token, err)
				vals = nil
				break
			}
			vals = append(vals, v)
		}
		if len(vals) != 3 {
			continue
		}
		sum := vals[0] + vals[1] + vals[2]
		if sum <= 0 {
			log.Printf("het-weights: ignoring %q (non-positive sum)", token)
			continue
		}
		alpha := vals[0] / sum
		beta := vals[1] / sum
		gamma := vals[2] / sum
		sets = append(sets, hetWeightSet{
			Alpha: alpha,
			Beta:  beta,
			Gamma: gamma,
			Label: formatWeightLabel(alpha, beta, gamma),
		})
	}
	return sets
}

func formatWeightLabel(alpha, beta, gamma float64) string {
	return fmt.Sprintf("a%s_b%s_g%s", compactFloat(alpha), compactFloat(beta), compactFloat(gamma))
}

func compactFloat(v float64) string {
	s := fmt.Sprintf("%.2f", v)
	s = strings.ReplaceAll(s, "-", "m")
	s = strings.ReplaceAll(s, ".", "p")
	return s
}

func formatHetPolicyID(mode hetpolicy.Mode, label string) string {
	modeSafe := strings.ReplaceAll(string(mode), "-", "_")
	if label != "" {
		return fmt.Sprintf("hetpolicy_%s_%s", modeSafe, label)
	}
	return fmt.Sprintf("hetpolicy_%s", modeSafe)
}

func resolveHetMode(token string) (hetpolicy.Mode, bool) {
	switch strings.ToLower(token) {
	case string(hetpolicy.ModeWeightedSum), "weighted-sum":
		return hetpolicy.ModeWeightedSum, true
	case string(hetpolicy.ModeEpsilonConstraint), "epsilon-constraint":
		return hetpolicy.ModeEpsilonConstraint, true
	case string(hetpolicy.ModeGreedyNormalised), "het-greedy-normalized", "greedy-normalised", "greedy-normalized":
		return hetpolicy.ModeGreedyNormalised, true
	default:
		return "", false
	}
}

func carbonScalerLambda(ciWeight float64) float64 {
	raw := clampFloat(ciWeight, 0, 1)
	return clampFloat(0.15+0.65*raw, 0, 0.85)
}

func calibrateHetWeights(ciWeight float64) (float64, float64, float64) {
	_ = ciWeight
	// Thesis-aligned weighting prioritises carbon (α) with equal emphasis on
	// runtime and queueing (β, γ). Keep a fixed triple to ensure reproducible sweeps.
	return 0.58, 0.21, 0.21
}

func writePerJobCSV(outDir, policy string, ciW float64, bs int, logs []core.LogEntry) {
	fn := fmt.Sprintf("%s_%.2f_%d_results.csv", policy, ciW, bs)
	path := filepath.Join(outDir, fn)
	f, err := os.Create(path)
	must(err)
	defer f.Close()

	w := csv.NewWriter(f)
	defer w.Flush()
	_ = w.Write([]string{"sched", "job_id", "site", "node", "submit", "start", "end", "wait_ms", "ci_cost"})
	for _, le := range logs {
		row := []string{
			policy,
			le.JobID,
			le.Site,
			le.Node,
			le.Submit.Format(time.RFC3339Nano),
			le.Start.Format(time.RFC3339Nano),
			le.End.Format(time.RFC3339Nano),
			strconv.FormatInt(le.WaitMS, 10),
			fmt.Sprintf("%.6f", le.CICost),
		}
		_ = w.Write(row)
	}
}

func writeSummary(outDir string, rows []SummaryRow) {
	// CSV
	csvPath := filepath.Join(outDir, "summary.csv")
	f, err := os.Create(csvPath)
	must(err)
	defer f.Close()
	w := csv.NewWriter(f)
	defer w.Flush()

	_ = w.Write([]string{
		"policy", "ci_weight", "batch_size",
		"total_ci_cost_g", "avg_ci_per_job_g", "cfp_g_per_cpu_hour",
		"avg_wait_s", "makespan_s", "elapsed_ms", "num_jobs",
		"alpha", "beta", "gamma",
		"alpha_mass", "lookahead_min", "duration_scale", "duration_overrides",
	})

	for _, r := range rows {
		_ = w.Write([]string{
			r.Policy,
			fmt.Sprintf("%.6f", r.CIWeight),
			strconv.Itoa(r.BatchSize),
			fmt.Sprintf("%.6f", r.TotalCICostG),
			fmt.Sprintf("%.6f", r.AvgCIPerJobG),
			fmt.Sprintf("%.6f", r.CFPgPerCPUHour),
			fmt.Sprintf("%.6f", r.AvgWaitS),
			fmt.Sprintf("%.6f", r.MakespanS),
			fmt.Sprintf("%.3f", r.ElapsedMs),
			strconv.Itoa(r.NumJobs),
			fmt.Sprintf("%.4f", r.Alpha),
			fmt.Sprintf("%.4f", r.Beta),
			fmt.Sprintf("%.4f", r.Gamma),
			fmt.Sprintf("%.4f", r.AlphaMass),
			strconv.Itoa(r.LookaheadMin),
			fmt.Sprintf("%.3f", r.DurationScale),
			r.DurationOverrides,
		})
	}

	// JSON (nice to have)
	jPath := filepath.Join(outDir, "summary.json")
	jf, err := os.Create(jPath)
	must(err)
	defer jf.Close()
	enc := json.NewEncoder(jf)
	enc.SetIndent("", "  ")
	_ = enc.Encode(rows)
}

func cleanResults(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, "_results.csv") || name == "summary.csv" || name == "summary.json" || strings.HasPrefix(name, "decisions") {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}

func summariseRun(policy string, ciW float64, bs int, logs []core.LogEntry, workloadByID map[string]core.Workload) SummaryRow {
	var totalCI float64
	var sumWaitMs int64
	var minStart, maxEnd time.Time

	var cfp metrics.CFPAggregate
	for i, le := range logs {
		totalCI += le.CICost
		sumWaitMs += le.WaitMS
		if i == 0 || le.Start.Before(minStart) {
			minStart = le.Start
		}
		if i == 0 || le.End.After(maxEnd) {
			maxEnd = le.End
		}
		// CFP fold
		w := workloadByID[le.JobID]
		rt := le.End.Sub(le.Start)
		cfp.Add(w.CPU, le.CICost, rt)
	}

	n := len(logs)
	avgWaitS := 0.0
	if n > 0 {
		avgWaitS = float64(sumWaitMs) / 1000.0 / float64(n)
	}
	makespanS := 0.0
	if !minStart.IsZero() && !maxEnd.IsZero() {
		makespanS = maxEnd.Sub(minStart).Seconds()
	}

	return SummaryRow{
		Policy:            policy,
		CIWeight:          ciW,
		BatchSize:         bs,
		TotalCICostG:      totalCI,
		AvgCIPerJobG:      safeDiv(totalCI, float64(n)),
		CFPgPerCPUHour:    cfp.CFPgPerCPUHour(),
		AvgWaitS:          avgWaitS,
		MakespanS:         makespanS,
		ElapsedMs:         0,
		NumJobs:           n,
		AlphaMass:         0, // filled at caller
		LookaheadMin:      0, // filled at caller
		DurationScale:     0, // filled at caller
		DurationOverrides: "",
	}
}

func clampFloat(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func safeDiv(a, b float64) float64 {
	if b == 0 {
		return 0
	}
	return a / b
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
