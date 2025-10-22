package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/g-uva/KubEnergySched/kespolicy/carbonscaler"
	"github.com/g-uva/KubEnergySched/kespolicy/hetpolicy"
	"github.com/g-uva/KubEnergySched/kespolicy/k8sched"
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
	AlphaMass         float64 `json:"alpha_mass"`
	LookaheadMin      int     `json:"lookahead_min"`
	DurationScale     float64 `json:"duration_scale"`
	DurationOverrides string  `json:"duration_overrides"`
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
					name: "kubernetes",
					run: func(w []core.Workload) ([]core.LogEntry, float64) {
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

						// prepare map for CFP
						workloadByID := map[string]core.Workload{}
						for _, j := range w {
							workloadByID[j.ID] = j
							sim.AddWorkload(j)
						}

						start := time.Now()
						sim.Run()
						elapsed := float64(time.Since(start).Milliseconds())

						logs := sim.Logs()
						writePerJobCSV(outDir, "kubernetes", ciW, bs, logs)

						s := summariseRun("kubernetes", ciW, bs, logs, workloadByID)
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
					name: "carbonscaler",
					run: func(w []core.Workload) ([]core.LogEntry, float64) {
						nodes := loader.LoadNodesFromCSV(nodesCSV)
						sites := loader.LoadSitesFromCSV(sitesCSV)
						loader.AttachSites(nodes, sites)

						pol := &carbonscaler.Policy{Cfg: carbonscaler.Config{Lambda: ciW}}
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
						writePerJobCSV(outDir, "carbonscaler", ciW, bs, logs)

						s := summariseRun("carbonscaler", ciW, bs, logs, workloadByID)
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
				specs = append(specs, struct {
					name string
					run  func([]core.Workload) ([]core.LogEntry, float64)
				}{
					name: "heterogeneous",
					run: func(w []core.Workload) ([]core.LogEntry, float64) {
						nodes := loader.LoadNodesFromCSV(nodesCSV)
						sites := loader.LoadSitesFromCSV(sitesCSV)
						loader.AttachSites(nodes, sites)

						sim := &core.BaseSim{}
						cfg := hetpolicy.DefaultConfig()
						cfg.Alpha = math.Max(ciW*0.4, 0.35)
						cfg.Beta = math.Max(cfg.Beta, 0.35)
						cfg.Gamma = math.Max(cfg.Gamma, 0.35)
						cfg.Delta = math.Max(cfg.Delta, 0.6)
						pol := &hetpolicy.Policy{
							Mode: mode,
							Cfg:  cfg,
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
						writePerJobCSV(outDir, "heterogeneous", ciW, bs, logs)

						s := summariseRun("heterogeneous", ciW, bs, logs, workloadByID)
						s.ElapsedMs = elapsed
						s.AlphaMass = alphaMass
						s.LookaheadMin = lookaheadMin
						s.DurationScale = durScale
						s.DurationOverrides = durationsFlag
						allSummaries = append(allSummaries, s)
						return logs, elapsed
					},
				})
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
