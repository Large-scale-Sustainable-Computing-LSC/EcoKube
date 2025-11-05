**Carbon Intensity**  
- Simulation: each node carries a `ci_profile` from `kubenergysched/config/nodes.csv:1-4`; `metrics.ComputeEnergyAndCarbon` interprets static/sine/randwalk profiles, applies site PUE/K, and returns CI-aware costs (`kubenergysched/pkg/metrics/cicost.go:17-172`). HetPolicy consumes those costs when ranking nodes (`kespolicy/hetpolicy/hetpolicy.go:137-214`), so CI directly influences scheduling.  
- Kubernetes: controller reads site defaults (`kubenergysched/config/sites.json:2-18`) and, if `FORECAST_BASE_URL` is set, pulls time-series forecasts via `pkg/providers/http_ci.go:21-57`; nodes inherit the site CI (`controller/main.go:112-144,443-496`). Decisions and traces invoke the same energy/CI model (`controller/main.go:521-545`), while `pkg/engine/engine.go:93-116` folds CI into the cost scored for every candidate. The FastAPI forecast service simply exposes the static ConfigMap now (`kpis/forecast_service/app.py:5-16`).

**Table 6.2 Parameters**  
- Job count `N_jobs`: no switch for 200/500/1000; the simulator always schedules the full workload (see `kubenergysched/pkg/generator/generator.go:111-147`), and the latest summaries still show `num_jobs=48` (`analysis/k8s_results/batch_200/summary.csv:2`). K8s replay just enforces `MAX_JOBS` (default 50) (`kubenergysched/workloads/make_jobs.py:38-204`). Spec missing.  
- Runtime distribution `D_size`: workload generation is a hand-crafted mix of uniform draws (`generator.go:111-145`); there is no log-normal/IQR/Pareto tail implementation. Spec missing.  
- Arrival process `D_arrival`: simulator uses `rand.ExpFloat64()` with fixed λ≈1 job/sec (`generator.go:136-138`); the replayer sleeps a constant `SUBMIT_EVERY_SEC` (`make_jobs.py:9-204`). There is no λ sweep or compound Poisson stress mode. Spec missing.  
- Resource classes `R`: workloads and nodes only expose CPU/GB (`pkg/core/Workload.go:8-23`, `kubenergysched/config/nodes.csv:1-4`); there is no GPU class, taint/selector logic, or perf-per-watt calibration. Spec missing.  
- Sites & nodes `S`: implemented—`cmd/gen_data.go:31-104` and `pkg/loader/sites.go:11-33` create/attach PUE and calibration factors, and the controller snapshot keeps them with each node (`controller/main.go:443-496`).  
- Policy weights `(α,β,γ)`: implemented. Simulator sweeps them (`cmd/run_sim.go:324-384`), and the controller reads overrides from `HETPOLICY_*` env vars (`controller/main.go:840-865`).  
- Kubernetes cap: no code or Helm value sets a 100-node cap; nothing in manifests or controller enforces it. Spec missing.

**Simulator↔K8s Parity**  
- Arrival rate: simulator relies on the fixed exponential inter-arrival noted above; the K8s replayer throttle is the static sleep loop (`make_jobs.py:194-204`). The λ set described in the table is not present.  
- Job type: simulator preserves the CSV `tag` (`pkg/loader/loader.go:72-111`); the replayer converts each CSV row into pod resources and command (`make_jobs.py:73-118`).  
- Batch size: simulator honours `SetScheduleBatchSize` (`pkg/core/basesim.go:44-137`). The K8s side records “batch” only in post-processing (`analysis/scripts/generate_k8s_tables.py:24-92`); there is no runtime mechanism that enforces a submission-wave size.  
- Site factors: simulator and controller both consume the same PUE/K data (`pkg/loader/sites.go:11-33`, `controller/main.go:443-496`); the ConfigMap ships the parity JSON (`k8s/helm/.../site-config-configmap.yaml:1-18`).  
- Carbon intensity: see Carbon Intensity section—parity is implemented, with the controller optionally calling the HTTP CI provider.  
- Horizon & warm-up: controller seeds Θ with a 2-hour horizon (`controller/main.go:119-137`), but the simulator never loads a Θ config (only helper `pkg/loader/loader.go:124-141` exists). Warm-up windows are not implemented on either side.  
- Random seeds: simulator data generation is seeded via `--seed` (`cmd/gen_data.go:31-57`, `generator.go:111`); Kubernetes replay processes jobs deterministically in CSV order but does not expose an explicit seed.

Next steps: 1) add configuration knobs so both simulator and replayer can run the 200/500/1000 job-count and batch scenarios the spec expects; 2) implement the required runtime/arrival distributions (log-normal, Pareto tail, λ sweeps) and GPU/resource-class support; 3) wire Θ/warm-up handling and the Kubernetes node-cap limit if those remain in scope.

### What needs to be done
- Job-count toggles and batch-size control are absent; both simulator and replayer need knobs for the 200/500/1000 sweeps and true submission waves.
- Workload realism isn’t there: no log-normal/IQR/Pareto runtime mix, no λ grid or bursty arrival process, and GPU/resource-class hints are ignored.
- Carbon-warmup/Theta parity gaps: simulator never loads Θ, there's no warm-up window, and the Kubernetes 100-node cap is unset.


Alright, so we must ingest real metrics. The goal is to implement a Scaphandre side-car with Prometheus and communicate everything to the Prometheus aggregator. Those metrics should be used to drive decisions, correct? How difficult it is to implement that? 