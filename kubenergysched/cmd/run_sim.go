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

    "github.com/g-uva/KubEnergySched/kubenergysched/pkg/core"
    "github.com/g-uva/KubEnergySched/kubenergysched/pkg/loader"
    "github.com/g-uva/KubEnergySched/kubenergysched/pkg/metrics"
    "github.com/g-uva/KubEnergySched/kubenergysched/pkg/types"
    "github.com/g-uva/KubEnergySched/kubenergysched/themis/policies/enginepolicy"
    "github.com/g-uva/KubEnergySched/kubenergysched/themis/policies/carbonscaler"
    "github.com/g-uva/KubEnergySched/kubenergysched/themis/policies/k8sched"
    "github.com/g-uva/KubEnergySched/kubenergysched/themis/policies/themisbase"
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
    var nodesCSV, wlCSV, outDir string
    var ciWeightsFlag, batchSizesFlag string
    var durationsFlag string
    var durScale float64
    var alphaMass float64
    var lookaheadMin int
    var tracePath string

    flag.StringVar(&nodesCSV, "nodes-csv", "config/nodes.csv", "path to nodes CSV")
    flag.StringVar(&wlCSV, "wl-csv", "config/workloads.csv", "path to workloads CSV")
    flag.StringVar(&outDir, "outdir", "", "output directory for per-run CSVs and summary (default results_YYYYmmdd_HHmmss)")
    flag.StringVar(&ciWeightsFlag, "ci-weights", "0.05,0.2,0.8,1.2", "comma-separated base CI weights to sweep")
    flag.StringVar(&batchSizesFlag, "batch-sizes", "32,128,256", "comma-separated batch sizes to sweep")
    flag.StringVar(&durationsFlag, "durations", "", "override job durations (seconds) as comma-separated list; assigned round-robin")
    flag.Float64Var(&durScale, "dur-scale", 1.0, "multiply all job durations by this factor")
    flag.Float64Var(&alphaMass, "alpha-mass", 1.0, "adaptive carbon weight multiplier for big jobs (0=off)")
    flag.IntVar(&lookaheadMin, "lookahead-min", 0, "look-ahead window in minutes (0=off)")
    flag.StringVar(&tracePath, "trace-jsonl", "", "append JSON decision traces to this file (use 'auto' for outdir/decisions.jsonl)")
    flag.Parse()

    if outDir == "" {
        outDir = fmt.Sprintf("results_%s", time.Now().Format("20060102_150405"))
    }
    must(os.MkdirAll(outDir, 0o755))

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
                    name: "engine",
                    run: func(w []core.Workload) ([]core.LogEntry, float64) {
                        nodes := loader.LoadNodesFromCSV(nodesCSV)
                        sites := loader.LoadSitesFromCSV("config/sites.csv")
                        loader.AttachSites(nodes, sites)

                        // Map ciW to engine weights: cost = E*eT + C*cT with E=1, C=ciW
                        engTheta := types.Theta{ThetaE: 1.0, ThetaC: ciW, Horizon: 2*time.Hour, Alpha: 0.95, EgressCapMB: 5e9, ERef: 10, CRef: 5}
                        pol := &enginepolicy.Policy{Cfg: enginepolicy.Config{Theta: engTheta}}
                        sim := &core.BaseSim{}
                        sim.Init(nodes, pol)
                        if tracer != nil { sim.SetTracer(tracer) }
                        sim.SetScheduleBatchSize(bs)
                        // CI costs are internal to enginepolicy; keep metrics for comparability if desired
                        sim.CICalc = func(n *core.SimulatedNode, w core.Workload, at time.Time) float64 { return metrics.ComputeCICost(n, w, at) }

                        workloadByID := map[string]core.Workload{}
                        for _, j := range w {
                            workloadByID[j.ID] = j
                            sim.AddWorkload(j)
                        }

                        start := time.Now(); sim.Run(); elapsed := float64(time.Since(start).Milliseconds())
                        logs := sim.Logs()
                        writePerJobCSV(outDir, "engine", ciW, bs, logs)
                        s := summariseRun("engine", ciW, bs, logs, workloadByID)
                        s.ElapsedMs = elapsed; s.AlphaMass = alphaMass; s.LookaheadMin = lookaheadMin; s.DurationScale = durScale; s.DurationOverrides = durationsFlag
                        allSummaries = append(allSummaries, s)
                        return logs, elapsed
                    },
                },
                {
                    name: "k8",
                    run: func(w []core.Workload) ([]core.LogEntry, float64) {
                        nodes := loader.LoadNodesFromCSV(nodesCSV)
                        sites := loader.LoadSitesFromCSV("config/sites.csv")
                        loader.AttachSites(nodes, sites)

                        pol := &k8sched.Policy{}
                        sim := &core.BaseSim{}
                        sim.Init(nodes, pol)
                        if tracer != nil { sim.SetTracer(tracer) }
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
                        writePerJobCSV(outDir, "k8", ciW, bs, logs)

                        s := summariseRun("k8", ciW, bs, logs, workloadByID)
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
                        sites := loader.LoadSitesFromCSV("config/sites.csv")
                        loader.AttachSites(nodes, sites)

                        pol := &carbonscaler.Policy{Cfg: carbonscaler.Config{Lambda: ciW}}
                        sim := &core.BaseSim{}
                        sim.Init(nodes, pol)
                        if tracer != nil { sim.SetTracer(tracer) }
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
                {
                    name: "themis_base",
                    run: func(w []core.Workload) ([]core.LogEntry, float64) {
                        nodes := loader.LoadNodesFromCSV(nodesCSV)
                        sites := loader.LoadSitesFromCSV("config/sites.csv")
                        loader.AttachSites(nodes, sites)

                        pol := &themisbase.Policy{
                            W:         themisbase.Weights{Carbon: ciW, Wait: 0.10, Util: 0.05},
                            AlphaMass: alphaMass,
                            Lookahead: time.Duration(lookaheadMin) * time.Minute,
                        }

                        sim := &core.BaseSim{}
                        sim.Init(nodes, pol)
                        if tracer != nil { sim.SetTracer(tracer) }
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
                        writePerJobCSV(outDir, "themis_base", ciW, bs, logs)

                        s := summariseRun("themis_base", ciW, bs, logs, workloadByID)
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
    if s == "" { return nil }
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
    if s == "" { return nil }
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
    if s == "" { return nil }
    fs := parseFloatSlice(s)
    out := make([]time.Duration, len(fs))
    for i, v := range fs { out[i] = time.Duration(v * float64(time.Second)) }
    return out
}

func writePerJobCSV(outDir, policy string, ciW float64, bs int, logs []core.LogEntry) {
    fn := fmt.Sprintf("%s_%.2f_%d_results.csv", policy, ciW, bs)
    path := filepath.Join(outDir, fn)
    f, err := os.Create(path)
    must(err)
    defer f.Close()

    w := csv.NewWriter(f)
    defer w.Flush()
    _ = w.Write([]string{"sched", "job_id", "node", "submit", "start", "end", "wait_ms", "ci_cost"})
    for _, le := range logs {
        row := []string{
            policy,
            le.JobID,
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

func summariseRun(policy string, ciW float64, bs int, logs []core.LogEntry, workloadByID map[string]core.Workload) SummaryRow {
    var totalCI float64
    var sumWaitMs int64
    var minStart, maxEnd time.Time

    var cfp metrics.CFPAggregate
    for i, le := range logs {
        totalCI += le.CICost
        sumWaitMs += le.WaitMS
        if i == 0 || le.Start.Before(minStart) { minStart = le.Start }
        if i == 0 || le.End.After(maxEnd) { maxEnd = le.End }
        // CFP fold
        w := workloadByID[le.JobID]
        rt := le.End.Sub(le.Start)
        cfp.Add(w.CPU, le.CICost, rt)
    }

    n := len(logs)
    avgWaitS := 0.0
    if n > 0 { avgWaitS = float64(sumWaitMs) / 1000.0 / float64(n) }
    makespanS := 0.0
    if !minStart.IsZero() && !maxEnd.IsZero() { makespanS = maxEnd.Sub(minStart).Seconds() }

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

func safeDiv(a, b float64) float64 { if b == 0 { return 0 }; return a / b }

func must(err error) { if err != nil { log.Fatal(err) } }
