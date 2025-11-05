package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand"
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
	JobCount          int     `json:"job_count"`
	ArrivalRate       float64 `json:"arrival_rate"`
	WarmupMinutes     float64 `json:"warmup_minutes"`
	ThetaE            float64 `json:"theta_e"`
	ThetaC            float64 `json:"theta_c"`
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
	var jobCountsFlag string
	var arrivalRatesFlag string
	var arrivalMode string
	var burstProb float64
	var burstMultiplier float64
	var warmupMin float64
	var thetaPath string
	var arrivalSeed int64

	flag.StringVar(&nodesCSV, "nodes-csv", "config/nodes.csv", "path to nodes CSV")
	flag.StringVar(&wlCSV, "wl-csv", "config/workloads.csv", "path to workloads CSV")
	flag.StringVar(&sitesCSV, "sites-csv", "", "path to sites CSV (defaults to nodes directory/sites.csv)")
	flag.StringVar(&outDir, "outdir", "", "output directory for per-run CSVs and summary (default kubenergysched/results)")
	flag.StringVar(&ciWeightsFlag, "ci-weights", "0.4", "comma-separated base CI weights to sweep")
	flag.StringVar(&batchSizesFlag, "batch-sizes", "64", "comma-separated batch sizes to sweep")
	flag.StringVar(&jobCountsFlag, "job-counts", "", "comma-separated job counts to evaluate (defaults to workload size)")
	flag.StringVar(&arrivalRatesFlag, "arrival-rates", "1.0", "comma-separated arrival rates (jobs/minute)")
	flag.StringVar(&arrivalMode, "arrival-mode", "poisson", "arrival process: poisson or bursty")
	flag.Float64Var(&burstProb, "arrival-burst-probability", 0.1, "burst probability when arrival-mode=bursty")
	flag.Float64Var(&burstMultiplier, "arrival-burst-multiplier", 3.0, "rate multiplier applied during bursts")
	flag.StringVar(&durationsFlag, "durations", "", "override job durations (seconds) as comma-separated list; assigned round-robin")
	flag.Float64Var(&durScale, "dur-scale", 1.0, "multiply all job durations by this factor")
	flag.Float64Var(&alphaMass, "alpha-mass", 1.0, "adaptive carbon weight multiplier for big jobs (0=off)")
	flag.IntVar(&lookaheadMin, "lookahead-min", 0, "look-ahead window in minutes (0=off)")
	flag.StringVar(&tracePath, "trace-jsonl", "", "append JSON decision traces to this file (use 'auto' for outdir/decisions.jsonl)")
	var hetModesFlag string
	flag.StringVar(&hetModesFlag, "het-modes", "het-weighted-sum", "comma-separated hetero policy modes (weighted-sum, epsilon-constraint, greedy-normalised)")
	var hetWeightsFlag string
	flag.StringVar(&hetWeightsFlag, "het-weights", "", "comma-separated alpha:beta:gamma sets for hetpolicy (e.g. '0.6:0.3:0.1'); leave empty or 'auto' to calibrate per CI weight")
	flag.Float64Var(&warmupMin, "warmup-min", 0, "warm-up window in minutes excluded from metrics (simulation only)")
	flag.StringVar(&thetaPath, "theta-yaml", "", "optional Theta YAML to align simulator parameters with the Kubernetes controller")
	flag.Int64Var(&arrivalSeed, "arrival-seed", 1337, "seed for synthetic arrival schedules")
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

	jobCounts := parseIntSlice(jobCountsFlag)
	if len(jobCounts) == 0 {
		jobCounts = []int{len(templateWl)}
	}
	arrivalRates := parseFloatSlice(arrivalRatesFlag)
	if len(arrivalRates) == 0 {
		arrivalRates = []float64{1.0}
	}
	arrivalMode = strings.ToLower(strings.TrimSpace(arrivalMode))
	if arrivalMode != "bursty" {
		arrivalMode = "poisson"
	}
	burstProb = clamp01(burstProb)
	if burstMultiplier <= 0 {
		burstMultiplier = 3.0
	}
	warmupDuration := time.Duration(warmupMin * float64(time.Minute))
	if warmupDuration < 0 {
		warmupDuration = 0
	}

	var theta loader.Theta
	var haveTheta bool
	if thetaPath != "" {
		t, err := loader.LoadTheta(thetaPath)
		if err != nil {
			log.Fatalf("load theta %s: %v", thetaPath, err)
		}
		theta = t
		haveTheta = true
	}

	var allSummaries []SummaryRow
	var templateStart time.Time
	if len(templateWl) > 0 {
		templateStart = templateWl[0].SubmitTime
	}

	for _, jobCount := range jobCounts {
		target := jobCount
		if target <= 0 || target > len(templateWl) {
			target = len(templateWl)
		}
		for _, arrivalRate := range arrivalRates {
			scenarioSeed := arrivalSeed + int64(target)*1000 + int64(arrivalRate*1000)
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

								applyArrivalSchedule(w, templateStart, bs, arrivalRate, arrivalMode, burstProb, burstMultiplier, scenarioSeed)
								workloadByID := make(map[string]core.Workload, len(w))
								for _, j := range w {
									workloadByID[j.ID] = j
									sim.AddWorkload(j)
								}

								start := time.Now()
								sim.Run()
								elapsed := float64(time.Since(start).Milliseconds())

								logs := sim.Logs()
								writePerJobCSV(outDir, policyID, ciW, bs, target, arrivalRate, logs)

								s := summariseRun(policyID, ciW, bs, target, arrivalRate, warmupDuration, logs, workloadByID)
								s.ElapsedMs = elapsed
								s.AlphaMass = alphaMass
								s.LookaheadMin = lookaheadMin
								s.DurationScale = durScale
								s.DurationOverrides = durationsFlag
								if haveTheta {
									s.ThetaE = theta.ThetaE
									s.ThetaC = theta.ThetaC
								}
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

								applyArrivalSchedule(w, templateStart, bs, arrivalRate, arrivalMode, burstProb, burstMultiplier, scenarioSeed)
								workloadByID := make(map[string]core.Workload, len(w))
								for _, j := range w {
									workloadByID[j.ID] = j
									sim.AddWorkload(j)
								}

								start := time.Now()
								sim.Run()
								elapsed := float64(time.Since(start).Milliseconds())

								logs := sim.Logs()
								writePerJobCSV(outDir, policyID, ciW, bs, target, arrivalRate, logs)

								s := summariseRun(policyID, ciW, bs, target, arrivalRate, warmupDuration, logs, workloadByID)
								s.ElapsedMs = elapsed
								s.Alpha = pol.Weights.Alpha
								s.Beta = pol.Weights.Beta
								s.Gamma = pol.Weights.Gamma
								s.AlphaMass = alphaMass
								s.LookaheadMin = lookaheadMin
								s.DurationScale = durScale
								s.DurationOverrides = durationsFlag
								if haveTheta {
									s.ThetaE = theta.ThetaE
									s.ThetaC = theta.ThetaC
								}
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

								applyArrivalSchedule(w, templateStart, bs, arrivalRate, arrivalMode, burstProb, burstMultiplier, scenarioSeed)
								workloadByID := make(map[string]core.Workload, len(w))
								for _, j := range w {
									workloadByID[j.ID] = j
									sim.AddWorkload(j)
								}

								start := time.Now()
								sim.Run()
								elapsed := float64(time.Since(start).Milliseconds())

								logs := sim.Logs()
								writePerJobCSV(outDir, policyID, ciW, bs, target, arrivalRate, logs)

								s := summariseRun(policyID, ciW, bs, target, arrivalRate, warmupDuration, logs, workloadByID)
								s.ElapsedMs = elapsed
								s.Alpha = pol.Weights.Alpha
								s.Beta = pol.Weights.Beta
								s.Gamma = pol.Weights.Gamma
								s.AlphaMass = alphaMass
								s.LookaheadMin = lookaheadMin
								s.DurationScale = durScale
								s.DurationOverrides = durationsFlag
								if haveTheta {
									s.ThetaE = theta.ThetaE
									s.ThetaC = theta.ThetaC
								}
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

								applyArrivalSchedule(w, templateStart, bs, arrivalRate, arrivalMode, burstProb, burstMultiplier, scenarioSeed)
								workloadByID := make(map[string]core.Workload, len(w))
								for _, j := range w {
									workloadByID[j.ID] = j
									sim.AddWorkload(j)
								}

								start := time.Now()
								sim.Run()
								elapsed := float64(time.Since(start).Milliseconds())

								logs := sim.Logs()
								writePerJobCSV(outDir, policyID, ciW, bs, target, arrivalRate, logs)

								s := summariseRun(policyID, ciW, bs, target, arrivalRate, warmupDuration, logs, workloadByID)
								s.ElapsedMs = elapsed
								s.AlphaMass = alphaMass
								s.LookaheadMin = lookaheadMin
								s.DurationScale = durScale
								s.DurationOverrides = durationsFlag
								if haveTheta {
									s.ThetaE = theta.ThetaE
									s.ThetaC = theta.ThetaC
								}
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

									applyArrivalSchedule(w, templateStart, bs, arrivalRate, arrivalMode, burstProb, burstMultiplier, scenarioSeed)
									workloadByID := make(map[string]core.Workload, len(w))
									for _, j := range w {
										workloadByID[j.ID] = j
										sim.AddWorkload(j)
									}

									start := time.Now()
									sim.Run()
									elapsed := float64(time.Since(start).Milliseconds())

									logs := sim.Logs()
									writePerJobCSV(outDir, policyID, ciW, bs, target, arrivalRate, logs)

									s := summariseRun(policyID, ciW, bs, target, arrivalRate, warmupDuration, logs, workloadByID)
									s.ElapsedMs = elapsed
									s.Alpha = weightCfg.Alpha
									s.Beta = weightCfg.Beta
									s.Gamma = weightCfg.Gamma
									s.AlphaMass = alphaMass
									s.LookaheadMin = lookaheadMin
									s.DurationScale = durScale
									s.DurationOverrides = durationsFlag
									if haveTheta {
										s.ThetaE = theta.ThetaE
										s.ThetaC = theta.ThetaC
									}
									allSummaries = append(allSummaries, s)
									return logs, elapsed
								},
							})
						}
					}

					for _, spec := range specs {
						wcopy := make([]core.Workload, target)
						copy(wcopy, templateWl[:target])
						_, _ = spec.run(wcopy)
					}
				}
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
	// Bias HetPolicy toward carbon-first decisions while keeping runtime/queueing sensitivity.
	return 0.72, 0.18, 0.10
}

func writePerJobCSV(outDir, policy string, ciW float64, bs int, jobCount int, arrivalRate float64, logs []core.LogEntry) {
	rateToken := strings.ReplaceAll(fmt.Sprintf("%.2f", arrivalRate), ".", "p")
	fn := fmt.Sprintf("%s_%.2f_%d_%d_%s_results.csv", policy, ciW, jobCount, bs, rateToken)
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
		"policy", "ci_weight", "batch_size", "job_count", "arrival_rate", "warmup_min", "theta_e", "theta_c",
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
			strconv.Itoa(r.JobCount),
			fmt.Sprintf("%.4f", r.ArrivalRate),
			fmt.Sprintf("%.2f", r.WarmupMinutes),
			fmt.Sprintf("%.4f", r.ThetaE),
			fmt.Sprintf("%.4f", r.ThetaC),
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

func applyArrivalSchedule(workloads []core.Workload, start time.Time, batchSize int, arrivalRate float64, mode string, burstProb, burstMultiplier float64, seed int64) {
	if len(workloads) == 0 {
		return
	}
	if batchSize <= 0 {
		batchSize = 1
	}
	if arrivalRate <= 0 {
		arrivalRate = 1.0
	}
	if start.IsZero() {
		start = workloads[0].SubmitTime
		if start.IsZero() {
			start = time.Now()
		}
	}
	rng := rand.New(rand.NewSource(seed))
	current := start
	for idx := 0; idx < len(workloads); {
		wave := batchSize
		if remaining := len(workloads) - idx; remaining < wave {
			wave = remaining
		}
		for j := 0; j < wave; j++ {
			workloads[idx+j].SubmitTime = current
		}
		idx += wave
		if idx >= len(workloads) {
			break
		}
		current = current.Add(sampleArrivalInterval(rng, arrivalRate, mode, burstProb, burstMultiplier))
	}
}

func sampleArrivalInterval(rng *rand.Rand, rate float64, mode string, burstProb, burstMultiplier float64) time.Duration {
	if rate <= 0 {
		rate = 1.0
	}
	minutes := rng.ExpFloat64() / rate
	if strings.EqualFold(mode, "bursty") && rng.Float64() < clamp01(burstProb) {
		mult := burstMultiplier
		if mult <= 0 {
			mult = 2.0
		}
		minutes /= mult
	}
	if minutes < 1e-4 {
		minutes = 1e-4
	}
	return time.Duration(minutes * float64(time.Minute))
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

func summariseRun(policy string, ciW float64, bs int, jobCount int, arrivalRate float64, warmup time.Duration, logs []core.LogEntry, workloadByID map[string]core.Workload) SummaryRow {
	effective := logs
	if warmup > 0 && len(logs) > 0 {
		minStart := logs[0].Start
		for _, le := range logs {
			if le.Start.Before(minStart) {
				minStart = le.Start
			}
		}
		boundary := minStart.Add(warmup)
		filtered := make([]core.LogEntry, 0, len(logs))
		for _, le := range logs {
			if le.Start.Before(boundary) {
				continue
			}
			filtered = append(filtered, le)
		}
		if len(filtered) > 0 {
			effective = filtered
		}
	}

	var totalCI float64
	var sumWaitMs int64
	var minStart, maxEnd time.Time

	var cfp metrics.CFPAggregate
	for i, le := range effective {
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

	n := len(effective)
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
		JobCount:          jobCount,
		ArrivalRate:       arrivalRate,
		WarmupMinutes:     warmup.Minutes(),
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

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
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
