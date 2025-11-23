package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/g-uva/EcoKube/kespolicy/carbonscaler"
	"github.com/g-uva/EcoKube/kespolicy/ecokube"
	"github.com/g-uva/EcoKube/kespolicy/k8sched"
	"github.com/g-uva/EcoKube/kespolicy/keids"
	"github.com/g-uva/EcoKube/kespolicy/topsis"
	"github.com/g-uva/EcoKube/kubenergysched/pkg/core"
	"github.com/g-uva/EcoKube/kubenergysched/pkg/loader"
	"github.com/g-uva/EcoKube/kubenergysched/pkg/metrics"
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
	CarbonMedianG     float64 `json:"carbon_median_g"`
	CarbonIqrG        float64 `json:"carbon_iqr_g"`
	WaitMedianS       float64 `json:"wait_median_s"`
	WaitIqrS          float64 `json:"wait_iqr_s"`
	Alpha             float64 `json:"alpha"`
	Beta              float64 `json:"beta"`
	Gamma             float64 `json:"gamma"`
	AlphaMass         float64 `json:"alpha_mass"`
	LookaheadMin      int     `json:"lookahead_min"`
	DurationScale     float64 `json:"duration_scale"`
	DurationOverrides string  `json:"duration_overrides"`
	Rep               int     `json:"rep"`
}

type ecoWeightSet struct {
	Alpha float64
	Beta  float64
	Gamma float64
	Label string
}

const (
	defaultRepetitions = 50
	seedStride         = 7919
)

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
	flag.StringVar(&ciWeightsFlag, "ci-weights", "0.35,0.40,0.45", "comma-separated base CI weights to sweep")
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
	var ecoModesFlag string
	flag.StringVar(&ecoModesFlag, "ecokube-modes", "het-weighted-sum", "comma-separated hetero policy modes (weighted-sum, epsilon-constraint, greedy-normalised)")
	var ecoWeightsFlag string
	flag.StringVar(&ecoWeightsFlag, "ecokube-weights", "", "comma-separated alpha:beta:gamma sets for ecokube (e.g. '0.6:0.3:0.1'); leave empty or 'auto' to calibrate per CI weight")
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
	ecoModes := parseEcoModes(ecoModesFlag)
	if len(ecoModes) > 1 {
		log.Printf("ecokube-modes: limiting to first mode %q for deterministic comparison", ecoModes[0])
		ecoModes = ecoModes[:1]
	}
	ecoWeightSets := parseEcoWeightSets(ecoWeightsFlag)

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
						run  func([]core.Workload, int64, int) ([]core.LogEntry, float64)
					}{
						{
							name: "k8s",
							run: func(w []core.Workload, seed int64, rep int) ([]core.LogEntry, float64) {
								const policyID = "k8s"
								nodes := loader.LoadNodesFromCSV(nodesCSV)
								sites := loader.LoadSitesFromCSV(sitesCSV)
								loader.AttachSites(nodes, sites)
								nodes = neutraliseSites(nodes)

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

								workloads := scrubPreferredSite(w)
								applyArrivalSchedule(workloads, templateStart, bs, arrivalRate, arrivalMode, burstProb, burstMultiplier, seed)
								workloadByID := make(map[string]core.Workload, len(workloads))
								for _, j := range workloads {
									workloadByID[j.ID] = j
									sim.AddWorkload(j)
								}

								start := time.Now()
								sim.Run()
								elapsed := float64(time.Since(start).Milliseconds())

								logs := sim.Logs()
								writePerJobCSV(outDir, policyID, ciW, bs, target, arrivalRate, rep, logs)

								s := summariseRun(policyID, ciW, bs, target, arrivalRate, warmupDuration, logs, workloadByID)
								s.ElapsedMs = elapsed
								s.AlphaMass = alphaMass
								s.LookaheadMin = lookaheadMin
								s.DurationScale = durScale
								s.DurationOverrides = durationsFlag
								s.Rep = rep
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
							run: func(w []core.Workload, seed int64, rep int) ([]core.LogEntry, float64) {
								const policyID = "keids"
								nodes := loader.LoadNodesFromCSV(nodesCSV)
								sites := loader.LoadSitesFromCSV(sitesCSV)
								loader.AttachSites(nodes, sites)
								nodes = neutraliseSites(nodes)

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

								workloads := scrubPreferredSite(w)
								applyArrivalSchedule(workloads, templateStart, bs, arrivalRate, arrivalMode, burstProb, burstMultiplier, seed)
								workloadByID := make(map[string]core.Workload, len(workloads))
								for _, j := range workloads {
									workloadByID[j.ID] = j
									sim.AddWorkload(j)
								}

								start := time.Now()
								sim.Run()
								elapsed := float64(time.Since(start).Milliseconds())

								logs := sim.Logs()
								writePerJobCSV(outDir, policyID, ciW, bs, target, arrivalRate, rep, logs)

								s := summariseRun(policyID, ciW, bs, target, arrivalRate, warmupDuration, logs, workloadByID)
								s.ElapsedMs = elapsed
								s.Alpha = pol.Weights.Alpha
								s.Beta = pol.Weights.Beta
								s.Gamma = pol.Weights.Gamma
								s.AlphaMass = alphaMass
								s.LookaheadMin = lookaheadMin
								s.DurationScale = durScale
								s.DurationOverrides = durationsFlag
								s.Rep = rep
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
							run: func(w []core.Workload, seed int64, rep int) ([]core.LogEntry, float64) {
								const policyID = "topsis"
								nodes := loader.LoadNodesFromCSV(nodesCSV)
								sites := loader.LoadSitesFromCSV(sitesCSV)
								loader.AttachSites(nodes, sites)
								nodes = neutraliseSites(nodes)

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

								workloads := scrubPreferredSite(w)
								applyArrivalSchedule(workloads, templateStart, bs, arrivalRate, arrivalMode, burstProb, burstMultiplier, seed)
								workloadByID := make(map[string]core.Workload, len(workloads))
								for _, j := range workloads {
									workloadByID[j.ID] = j
									sim.AddWorkload(j)
								}

								start := time.Now()
								sim.Run()
								elapsed := float64(time.Since(start).Milliseconds())

								logs := sim.Logs()
								writePerJobCSV(outDir, policyID, ciW, bs, target, arrivalRate, rep, logs)

								s := summariseRun(policyID, ciW, bs, target, arrivalRate, warmupDuration, logs, workloadByID)
								s.ElapsedMs = elapsed
								s.Alpha = pol.Weights.Alpha
								s.Beta = pol.Weights.Beta
								s.Gamma = pol.Weights.Gamma
								s.AlphaMass = alphaMass
								s.LookaheadMin = lookaheadMin
								s.DurationScale = durScale
								s.DurationOverrides = durationsFlag
								s.Rep = rep
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
							run: func(w []core.Workload, seed int64, rep int) ([]core.LogEntry, float64) {
								const policyID = "carbonscaler"
								lambda := carbonScalerLambda(ciW)
								nodes := loader.LoadNodesFromCSV(nodesCSV)
								sites := loader.LoadSitesFromCSV(sitesCSV)
								loader.AttachSites(nodes, sites)
								nodes = neutraliseSites(nodes)

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

								workloads := scrubPreferredSite(w)
								applyArrivalSchedule(workloads, templateStart, bs, arrivalRate, arrivalMode, burstProb, burstMultiplier, seed)
								workloadByID := make(map[string]core.Workload, len(workloads))
								for _, j := range workloads {
									workloadByID[j.ID] = j
									sim.AddWorkload(j)
								}

								start := time.Now()
								sim.Run()
								elapsed := float64(time.Since(start).Milliseconds())

								logs := sim.Logs()
								writePerJobCSV(outDir, policyID, ciW, bs, target, arrivalRate, rep, logs)

								s := summariseRun(policyID, ciW, bs, target, arrivalRate, warmupDuration, logs, workloadByID)
								s.ElapsedMs = elapsed
								s.AlphaMass = alphaMass
								s.LookaheadMin = lookaheadMin
								s.DurationScale = durScale
								s.DurationOverrides = durationsFlag
								s.Rep = rep
								if haveTheta {
									s.ThetaE = theta.ThetaE
									s.ThetaC = theta.ThetaC
								}
								allSummaries = append(allSummaries, s)
								return logs, elapsed
							},
						},
					}

					for _, mode := range ecoModes {
						mode := mode
						weights := ecoWeightSets
						if len(weights) == 0 {
							alpha, beta, gamma := calibrateEcoWeights(ciW)
							weights = []ecoWeightSet{{
								Alpha: alpha,
								Beta:  beta,
								Gamma: gamma,
								Label: formatWeightLabel(alpha, beta, gamma),
							}}
						}
						for _, weightCfg := range weights {
							weightCfg := weightCfg
							policyID := "ecokube"
							if len(ecoWeightSets) > 0 {
								policyID = formatEcoKubeID(mode, weightCfg.Label)
							}
							specs = append(specs, struct {
								name string
								run  func([]core.Workload, int64, int) ([]core.LogEntry, float64)
							}{
								name: policyID,
								run: func(w []core.Workload, seed int64, rep int) ([]core.LogEntry, float64) {
									nodes := loader.LoadNodesFromCSV(nodesCSV)
									sites := loader.LoadSitesFromCSV(sitesCSV)
									loader.AttachSites(nodes, sites)

									sim := &core.BaseSim{}
									cfg := ecokube.DefaultConfig()
									cfg.Delta = 0.0
									cfg.Alpha = weightCfg.Alpha
									cfg.Beta = weightCfg.Beta
									cfg.Gamma = weightCfg.Gamma
									pol := &ecokube.Policy{
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

									applyArrivalSchedule(w, templateStart, bs, arrivalRate, arrivalMode, burstProb, burstMultiplier, seed)
									workloadByID := make(map[string]core.Workload, len(w))
									for _, j := range w {
										workloadByID[j.ID] = j
										sim.AddWorkload(j)
									}

									start := time.Now()
									sim.Run()
									elapsed := float64(time.Since(start).Milliseconds())

									logs := sim.Logs()
									writePerJobCSV(outDir, policyID, ciW, bs, target, arrivalRate, rep, logs)

									s := summariseRun(policyID, ciW, bs, target, arrivalRate, warmupDuration, logs, workloadByID)
									s.ElapsedMs = elapsed
									s.Alpha = weightCfg.Alpha
									s.Beta = weightCfg.Beta
									s.Gamma = weightCfg.Gamma
									s.AlphaMass = alphaMass
									s.LookaheadMin = lookaheadMin
									s.DurationScale = durScale
									s.DurationOverrides = durationsFlag
									s.Rep = rep
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

					for rep := 0; rep < defaultRepetitions; rep++ {
						repSeed := scenarioSeed + int64(rep)*seedStride
						for _, spec := range specs {
							wcopy := make([]core.Workload, target)
							copy(wcopy, templateWl[:target])
							_, _ = spec.run(wcopy, repSeed, rep)
						}
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

func parseEcoModes(raw string) []ecokube.Mode {
	tokens := parseStringSlice(raw)
	if len(tokens) == 0 {
		return []ecokube.Mode{ecokube.ModeWeightedSum}
	}
	seen := map[ecokube.Mode]struct{}{}
	modes := make([]ecokube.Mode, 0, len(tokens))
	for _, token := range tokens {
		if mode, ok := resolveEcoMode(token); ok {
			if _, exists := seen[mode]; !exists {
				seen[mode] = struct{}{}
				modes = append(modes, mode)
			}
		} else {
			log.Printf("unknown het-mode %q ignored", token)
		}
	}
	if len(modes) == 0 {
		return []ecokube.Mode{ecokube.ModeWeightedSum}
	}
	return modes
}

func parseEcoWeightSets(raw string) []ecoWeightSet {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.EqualFold(raw, "auto") {
		return nil
	}
	tokens := strings.Split(raw, ",")
	sets := make([]ecoWeightSet, 0, len(tokens))
	for _, token := range tokens {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		parts := strings.Split(token, ":")
		if len(parts) != 3 {
			log.Printf("ecokube-weights: expected alpha:beta:gamma, got %q", token)
			continue
		}
		vals := make([]float64, 0, 3)
		for _, part := range parts {
			v, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
			if err != nil {
				log.Printf("ecokube-weights: ignoring %q (%v)", token, err)
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
			log.Printf("ecokube-weights: ignoring %q (non-positive sum)", token)
			continue
		}
		alpha := vals[0] / sum
		beta := vals[1] / sum
		gamma := vals[2] / sum
		sets = append(sets, ecoWeightSet{
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

func formatEcoKubeID(mode ecokube.Mode, label string) string {
	modeSafe := strings.ReplaceAll(string(mode), "-", "_")
	if label != "" {
		return fmt.Sprintf("ecokube_%s_%s", modeSafe, label)
	}
	return fmt.Sprintf("ecokube_%s", modeSafe)
}

func resolveEcoMode(token string) (ecokube.Mode, bool) {
	switch strings.ToLower(token) {
	case string(ecokube.ModeWeightedSum), "weighted-sum":
		return ecokube.ModeWeightedSum, true
	case string(ecokube.ModeEpsilonConstraint), "epsilon-constraint":
		return ecokube.ModeEpsilonConstraint, true
	case string(ecokube.ModeGreedyNormalised), "het-greedy-normalized", "greedy-normalised", "greedy-normalized":
		return ecokube.ModeGreedyNormalised, true
	default:
		return "", false
	}
}

func carbonScalerLambda(ciWeight float64) float64 {
	raw := clampFloat(ciWeight, 0, 1)
	return clampFloat(0.15+0.65*raw, 0, 0.85)
}

func calibrateEcoWeights(ciWeight float64) (float64, float64, float64) {
	// Bias EcoKube toward heterogeneity-aware runtime while keeping a carbon guard.
	carbon := clampFloat(0.25+0.05*(0.4-ciWeight), 0.22, 0.30)
	timeW := 0.40
	energyW := 1 - carbon - timeW
	if energyW < 0.25 {
		energyW = 0.25
		timeW = 1 - carbon - energyW
	}
	return carbon, timeW, energyW
}

func neutraliseSites(nodes []*core.SimulatedNode) []*core.SimulatedNode {
	out := make([]*core.SimulatedNode, len(nodes))
	for i, n := range nodes {
		if n == nil {
			continue
		}
		copyNode := *n
		copyNode.Site = &core.Site{
			ID:              "neutral",
			PUE:             1.3,
			K:               1.0,
			CarbonIntensity: 450.0,
			CIRegion:        "neutral",
		}
		copyNode.SiteID = ""
		out[i] = &copyNode
	}
	return out
}

func scrubPreferredSite(workloads []core.Workload) []core.Workload {
	out := make([]core.Workload, len(workloads))
	for i, w := range workloads {
		copyW := w
		if len(copyW.Labels) > 0 {
			labels := make(map[string]string, len(copyW.Labels))
			for k, v := range copyW.Labels {
				if strings.EqualFold(k, "preferred_site") {
					continue
				}
				labels[k] = v
			}
			copyW.Labels = labels
		}
		out[i] = copyW
	}
	return out
}

func writePerJobCSV(outDir, policy string, ciW float64, bs int, jobCount int, arrivalRate float64, rep int, logs []core.LogEntry) {
	rateToken := strings.ReplaceAll(fmt.Sprintf("%.2f", arrivalRate), ".", "p")
	fn := fmt.Sprintf("%s_%.2f_%d_%d_%s_rep%02d_results.csv", policy, ciW, jobCount, bs, rateToken, rep)
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
		"total_ci_cost_g", "avg_ci_per_job_g", "carbon_median_g", "carbon_iqr_g", "cfp_g_per_cpu_hour",
		"avg_wait_s", "wait_median_s", "wait_iqr_s", "makespan_s", "elapsed_ms", "num_jobs",
		"alpha", "beta", "gamma",
		"alpha_mass", "lookahead_min", "duration_scale", "duration_overrides",
		"rep",
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
			fmt.Sprintf("%.6f", r.CarbonMedianG),
			fmt.Sprintf("%.6f", r.CarbonIqrG),
			fmt.Sprintf("%.6f", r.CFPgPerCPUHour),
			fmt.Sprintf("%.6f", r.AvgWaitS),
			fmt.Sprintf("%.6f", r.WaitMedianS),
			fmt.Sprintf("%.6f", r.WaitIqrS),
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
			strconv.Itoa(r.Rep),
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
	waitSamples := make([]float64, 0, len(effective))
	ciSamples := make([]float64, 0, len(effective))
	for i, le := range effective {
		totalCI += le.CICost
		sumWaitMs += le.WaitMS
		waitSamples = append(waitSamples, float64(le.WaitMS)/1000.0)
		ciSamples = append(ciSamples, le.CICost)
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
	medianWait := median(waitSamples)
	waitSpread := iqr(waitSamples)
	if n > 0 {
		if medianWait == 0 {
			medianWait = float64(sumWaitMs) / 1000.0 / float64(n)
		}
	}
	makespanS := 0.0
	if !minStart.IsZero() && !maxEnd.IsZero() {
		makespanS = maxEnd.Sub(minStart).Seconds()
	}
	ciMedian := median(ciSamples)
	ciSpread := iqr(ciSamples)
	if ciMedian == 0 && n > 0 {
		ciMedian = safeDiv(totalCI, float64(n))
	}

	return SummaryRow{
		Policy:            policy,
		CIWeight:          ciW,
		BatchSize:         bs,
		JobCount:          jobCount,
		ArrivalRate:       arrivalRate,
		WarmupMinutes:     warmup.Minutes(),
		TotalCICostG:      totalCI,
		AvgCIPerJobG:      ciMedian,
		CFPgPerCPUHour:    cfp.CFPgPerCPUHour(),
		AvgWaitS:          medianWait,
		CarbonMedianG:     ciMedian,
		CarbonIqrG:        ciSpread,
		WaitMedianS:       medianWait,
		WaitIqrS:          waitSpread,
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

func median(values []float64) float64 {
	return quantile(values, 0.5)
}

func iqr(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	return quantile(values, 0.75) - quantile(values, 0.25)
}

func quantile(values []float64, q float64) float64 {
	if len(values) == 0 {
		return 0
	}
	if q <= 0 {
		min := values[0]
		for _, v := range values[1:] {
			if v < min {
				min = v
			}
		}
		return min
	}
	if q >= 1 {
		max := values[0]
		for _, v := range values[1:] {
			if v > max {
				max = v
			}
		}
		return max
	}
	cp := append([]float64(nil), values...)
	sort.Float64s(cp)
	position := q * float64(len(cp)-1)
	lower := int(math.Floor(position))
	upper := int(math.Ceil(position))
	if lower == upper {
		return cp[lower]
	}
	weight := position - float64(lower)
	return cp[lower]*(1-weight) + cp[upper]*weight
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
