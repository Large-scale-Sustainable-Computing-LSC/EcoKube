package engine

import (
    "context"
    "fmt"
    "sort"
    "time"

    "github.com/g-uva/KubEnergySched/hermes/pkg/providers"
    "github.com/g-uva/KubEnergySched/hermes/pkg/types"
)

type Deps struct {
    CI      providers.CIProvider // can be nil
    Theta   types.Theta
    Refs    types.RefScales
    Weights types.Weights
    Now     func() time.Time // inject clock for parity
}

// ScoreNodes computes cost per candidate and returns scores and per-node traces.
// It applies the same feasibility gates and normalisation used by Schedule.
func ScoreNodes(ctx context.Context, j types.Job, nodes []types.NodeSnapshot, d Deps) (map[string]float64, map[string]types.DecisionTrace) {
    scores := make(map[string]float64)
    traces := make(map[string]types.DecisionTrace)
    now := time.Now
    if d.Now != nil { now = d.Now }

    cands, _ := buildCandidateSet(j, nodes, d.Theta.Alpha, d.Theta.EgressCapMB, now())
    for _, n := range cands {
        eT, cT, J, usedForecast := evalCost(ctx, j, &n, d)
        scores[n.ID] = J
        traces[n.ID] = types.DecisionTrace{
            JobID: j.ID, Site: n.Site.Name, Node: n.ID,
            ENorm: eT, CNorm: cT, Cost: J,
            ThetaE: d.Theta.ThetaE, ThetaC: d.Theta.ThetaC,
            ForecastUsed: usedForecast,
        }
    }
    return scores, traces
}

// Public entry: SAME for simulation and K8s
func Schedule(ctx context.Context, j types.Job, nodes []types.NodeSnapshot, d Deps) (types.Decision, types.DecisionTrace, error) {
    // 1) Score all candidates (includes feasibility)
    scores, traces := ScoreNodes(ctx, j, nodes, d)

    // If no candidates, return with rejection
    if len(scores) == 0 {
        return types.Decision{}, types.DecisionTrace{
            JobID: j.ID, RejectReason: "no feasible candidates",
            ThetaE: d.Theta.ThetaE, ThetaC: d.Theta.ThetaC, Fallback: true,
        }, fmt.Errorf("no feasible candidates")
    }

    // 3) Select argmin with deterministic tie-break
    id, ok := argMinStable(scores)
    if !ok {
        return types.Decision{}, types.DecisionTrace{
            JobID: j.ID, RejectReason: "no_scores", Fallback: true,
        }, fmt.Errorf("no scores")
    }

    // 4) Optional elasticity (MVP: scale=1)
    dec := types.Decision{NodeID: id, Scale: 1}
    return dec, traces[id], nil
}

// ---------- helpers below ----------

func buildCandidateSet(j types.Job, nodes []types.NodeSnapshot, alpha, egressCapMB float64, now time.Time) ([]types.NodeSnapshot, map[string]string) {
    var out []types.NodeSnapshot
    rejects := map[string]string{}
    for _, n := range nodes {
        if !canAccept(n, j) { rejects[n.ID] = "capacity"; continue }
        if probSLO(j) < alpha { rejects[n.ID] = "slo"; continue }
        if estimateEgressMB(j, n) > egressCapMB { rejects[n.ID] = "egress"; continue }
        out = append(out, n)
    }
    return out, rejects
}

func canAccept(n types.NodeSnapshot, j types.Job) bool {
    return j.CPU <= n.AvailableCPU && j.MemoryGB <= n.AvailableGB
}
func probSLO(j types.Job) float64 { return 1.0 } // MVP stub
func estimateEgressMB(j types.Job, n types.NodeSnapshot) float64 {
    // MVP: 0 unless a tag forces remote data
    if j.Tags != nil && j.Tags["remote_data"] == "true" { return 1e9 }
    return 0
}

func evalCost(ctx context.Context, j types.Job, n *types.NodeSnapshot, d Deps) (eT, cT, J float64, usedForecast bool) {
    // Forecast CI: use first step; fallback to site CI
    CIg := n.Site.CarbonIntensity
    if d.CI != nil {
        if arr, err := d.CI.ForecastCI(ctx, n.Site.Region, d.Theta.Horizon); err == nil && len(arr) > 0 {
            CIg, usedForecast = arr[0], true
        }
    }
    // Energy estimate (kWh): k_s * power * hours
    pw := n.Metrics["power_w_mean"]
    if pw <= 0 {
        peak := parseLabelFloat(n.Labels["peak_power_w"], 120)
        pw = 0.6 * peak
    }
    EkWh := n.Site.K * pw * d.Theta.Horizon.Hours() / 1000.0
    // Carbon (kg): E * PUE * CI[g]/1000
    Ckg := EkWh * n.Site.PUE * (CIg / 1000.0)
    // Normalise
    if d.Refs.ERef <= 0 { d.Refs.ERef = 1 }
    if d.Refs.CRef <= 0 { d.Refs.CRef = 1 }
    eT = EkWh / d.Refs.ERef
    cT = Ckg  / d.Refs.CRef
    // Cost
    J = d.Weights.E*eT + d.Weights.C*cT
    return
}

func argMinStable(scores map[string]float64) (string, bool) {
    if len(scores) == 0 { return "", false }
    type kv struct{ id string; v float64 }
    list := make([]kv, 0, len(scores))
    for id, v := range scores { list = append(list, kv{id, v}) }
    sort.Slice(list, func(i, j int) bool {
        if list[i].v == list[j].v { return list[i].id < list[j].id }
        return list[i].v < list[j].v
    })
    return list[0].id, true
}

func parseLabelFloat(s string, def float64) float64 {
    var f float64
    _, err := fmt.Sscanf(s, "%f", &f)
    if err != nil { return def }
    return f
}

func joinReasons(m map[string]string) string {
    if len(m) == 0 { return "" }
    out := ""
    for id, r := range m {
        out += id + ":" + r + ";"
    }
    return out
}
