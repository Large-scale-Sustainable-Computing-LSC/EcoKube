### 🔋 EcoKube - Scheduling Framework for Heterogeneous Multi-Cluster Research Infrastructures
EcoKube is the sustainability-aware scheduling framework for Heterogeneous RIs. The goal is to integrate heterogeneous infrastructures while optimising **sustainability** outcomes across simulation and Kubernetes replay tracks. It orchestrates the KesPolicies suite (located under `kespolicy/`) to compare heterogeneous scheduling strategies consistently.

## How to use
- **1. Prepare inputs** – Generate or update `config/nodes.csv`, `config/workloads.csv`, and `config/sites.csv` with the new workload knobs:
  ```bash
  cd ecokube
  go run ./cmd/gen_data.go \
    --nodes-out=config/nodes.csv \
    --workloads-out=config/workloads.csv \
    --sites-csv-out=config/sites.csv \
    --sites-json-out=config/sites.json \
    --seed=42 \
    --jobs=1000 \
    --arrival-rate=1.0 \
    --batch-size=64 \
    --arrival-mode=bursty \
    --burst-probability=0.25 \
    --burst-multiplier=3.0 \
    --gpu-share=0.15
  ```
  The tool now emits GPU-labelled nodes and workloads with additional columns (`class`, `resource_class`, `gpu_count`, `preferred_site`) plus the node-level `device_class` hint that drive both the simulator and the Kubernetes replayer. Adjust the knobs to mirror the scenarios you need, or reuse the committed defaults.
- **2. Run the simulator sweep** – Launch the richer sweep directly:
  ```bash
  cd ecokube
  go run ./cmd/run_sim.go \
    --nodes-csv=config/nodes.csv \
    --wl-csv=config/workloads.csv \
    --sites-csv=config/sites.csv \
    --outdir=analysis/sim_results_latest \
    --ci-weights=0.2,0.4,0.6,0.8 \
    --batch-sizes=200,500,1000 \
    --job-counts=200,500,1000 \
    --arrival-rates=0.5,1.0,1.5 \
    --arrival-mode=bursty \
    --arrival-burst-probability=0.25 \
    --arrival-burst-multiplier=2.5 \
    --warmup-min=30 \
    --arrival-seed=1337
  ```
  The simulator now rewrites submit timestamps per scenario, honours the warm-up window when summarising metrics, and captures the extra knobs (job-count, arrival-rate, Θ) inside `summary.csv`. Use a fresh `--outdir` (e.g. `analysis/sim_runs/run_$(date +%Y%m%d_%H%M%S)`) when you want to archive multiple sweeps.
  Add `--het-w-fit=0.2` (or another value) to tune the new device/accelerator fit term so sweeps stay aligned with the controller’s `ECOKUBE_W_FIT` setting.
- **3. Optionally sync multiple sweeps** – `./ecokube/cmd/sweep_sim.sh` still works; export the same environment variables (`SWEEP_CI_WEIGHTS`, `SWEEP_BATCH_SIZES`, `SWEEP_OUT_PREFIX`) to mirror the command above.
- **3. Collect Kubernetes traces (optional)** – Replay the batch via `k8s/replay_workloads.yaml`, then export decisions to `analysis/k8s_results_latest/decisions.jsonl`. The simulator notebooks automatically harmonise both sources if the JSONL is present.
- **4. Launch the analysis notebook** – Open `analysis/jupyter/output_capture.ipynb` (or `final_analysis.executed.ipynb`) in Jupyter, run all cells, and review the generated tables, plots, and evaluation metrics.
- **5. Compare policies** – The notebook materialises the carbon and timeliness metrics mandated by the thesis (CFP, SCI, makespan, latency, scheduler overhead, throughput, average energy per job) so both pathways can be contrasted consistently.

**If we want the convenient `hetsched` key to execute commands:**
```
echo 'export PATH="$PATH:/home/goncalo/KubeEnergyScheduler/k8s/scripts"' >> ~/.bashrc
source ~/.bashrc

hetsched() {
  /home/goncalo/KubeEnergyScheduler/k8s/scripts/cluster.sh "$@"
}
```

### Kubernetes replay quick start
The replay track mirrors the simulator while exercising the live EcoKube and CarbonScaler controllers.

1. **Create the Kind cluster** (multi-node, labelled): `kind create cluster --name hetsched --config k8s/kind/multi-node.yaml`.
2. **Load fresh controller/replayer images**: `kind load docker-image --name hetsched goncaloferreirauva/ci-aware-controller:latest` and `goncaloferreirauva/workload-replayer:latest`.
3. **Install the Helm stack** (EcoKube): `./k8s/scripts/cluster.sh helm-up`. The controller honours `CIW_NODE_CAP` (default `100`) to mimic the simulator’s node limit; override it via `kubectl -n workloads set env deploy/ci-aware-controller CIW_NODE_CAP=<cap>`. Submit batches with `./k8s/scripts/cluster.sh replay` and watch their `Job` objects with `./k8s/scripts/cluster.sh jobs` (pods via `status`).
4. **Export EcoKube decisions**: `RESULT_DIR=$PWD/analysis/k8s_results_latest/ecokube ./k8s/scripts/cluster.sh fetch`.
5. **Switch to CarbonScaler**: `kubectl -n workloads set env deploy/ci-aware-controller SCHEDULER_POLICY=carbonscaler` and rerun `helm-up`.
6. **Export CarbonScaler decisions**: `RESULT_DIR=$PWD/analysis/k8s_results_latest/carbonscaler ./k8s/scripts/cluster.sh fetch`.
7. **Aggregate + plots**: `python analysis/scripts/aggregate_k8s.py --ecokube analysis/k8s_results_latest/ecokube/decisions.jsonl --carbonscaler analysis/k8s_results_latest/carbonscaler/decisions.jsonl --output analysis/k8s_results --figures-dir analysis/figures/k8s`. This writes CSV exports to `analysis/k8s_results/` and drops the PNGs in `analysis/figures/k8s/`.
8. **Preview notebooks**: `analysis/jupyter/sim_analysis.ipynb` for the simulator, `analysis/jupyter/k8s_analysis.ipynb` for the replay.

Outputs are mirrored to `analysis/k8s_results/` (CSV + PNG). All generated artefacts now land under the analysis tree:
- `analysis/figures/sim` and `analysis/figures/k8s` collect every simulator/replay PNG (Pareto, tail-latency violin, makespan bars; ignored via `.gitignore`).
- `analysis/k8s_results` stores replay CSV exports (`batch_*/summary.csv` plus `per_job.csv`).
- `analysis/tables` centralises the LaTeX fragments emitted by `generate_{sim,k8s}_tables.py`.

## Evaluation metrics
The notebook implements the Section 6.2.2 definitions over the harmonised per-job traces:
- **Carbon Footprint (CFP)** – $CFP_j = Σ_t E_{j,t} · CI_{s,t} / 1000$, reported per job and per batch in grams and kilograms.
- **Software Carbon Intensity (SCI)** – $SCI = Σ_j CFP_j / R`, where `R$ is the count of completed jobs.
- **Makespan & latency** – $Makespan = max_j C_j − min_j A_j$, $Latency = (1/N) Σ_j (S_j − A_j)$ with arrivals $A$, starts $S$, and completions $C$.
- **Scheduler overhead** – Average scheduling cost per job, using per-job latency when available and `elapsed_ms / N` from simulator summaries otherwise.
- **Throughput** – `N / wall_time`, where `wall_time` equals the measured makespan.
- **Energy per job** – `1/N Σ_{j,t} E_{j,t}`, derived from direct telemetry when present and otherwise estimated from node power/PUE metadata.

Run the notebook after each simulator/Kubernetes export; `evaluation_metrics` in the notebook contains the consolidated table ready for reporting.

## Generating simulator inputs
`cmd/gen_data.go` now exposes all workload realism knobs in a single place:
- `--jobs`, `--arrival-rate`, `--arrival-mode`, `--burst-probability`, `--burst-multiplier`, `--batch-size` govern the arrival process.
- `--gpu-share`, `--seed`, and the log-normal/Pareto quantiles shape the job mix and heavy tail.
- `config/workloads.csv` gains `class`, `preferred_site`, `resource_class`, and `gpu_count`; `config/nodes.csv` carries `device_class`, an optional `labels` column (e.g. `gpu=true`), and `peak_power_w`.

The Kubernetes replayer reads the same CSV, applies the precise submission timestamps, requests GPUs when `gpu_count>0`, and labels pods with the resource hints the simulator uses (`resource_class`, `requires_gpu`, `preferred_site`). The controller ConfigMap (`sites.json`) still provides the static CI/PUE defaults unless a live forecast endpoint is configured.

## KesPolicies suite
EcoKube evaluates the following KesPolicies implementations:
- **K8sPolicy** – Baseline bin-pack strategy derived from upstream Kubernetes, used as the carbon-unaware reference.
- **KEIDSPolicy** – Weighted composite policy inspired by KEIDS, balancing carbon intensity, runtime, and interference with calibrated weights.
- **TOPSISPolicy** – Technique for Order Preference by Similarity to Ideal Solution, using the same ${α, β, γ}$ weights but ranking nodes via vector normalisation.
- **EcoKube** – Heterogeneity-aware policy that accounts for node/site diversity while applying the calibrated thesis weights (including optional δ terms).
- **CarbonScalerPolicy** – Replay-only policy mirroring the CarbonScaler controller when Kubernetes trace exports are available for comparison.

## Kubernetes pathway
The Kubernetes replay track mirrors the simulator while exercising real scheduling policies (EcoKube and CarbonScaler). The helper script `k8s/scripts/cluster.sh` automates the end-to-end lifecycle:

1. **Build container images** – Push updated controller, replayer, and metrics-agent images (`ecokube/controller`, `ecokube/workloads`, `k8s/images/ciw-metrics-agent`).
2. **Bootstrap services** – `./k8s/scripts/cluster.sh bootstrap` prepares the namespace, refreshes ConfigMaps (`nodes.csv`, `workloads.csv`, `sites.json`), and deploys the CI-aware controller with EcoKube enabled by default.
3. **Replay the workloads** – `./k8s/scripts/cluster.sh replay` submits the batch via the workload replayer. Each Job includes an in-pod Prometheus metrics agent (port `9101`) sharing the process namespace so the exporter sees the workload’s processes.
4. **Collect traces** – `./k8s/scripts/cluster.sh fetch [output]` copies `/var/log/ciw/decisions.jsonl` into `analysis/k8s_results_latest/decisions.jsonl` (or a custom path) ready for the notebooks. When using the Helm deployment, set `KES_CONTROLLER_DEPLOYMENT=ci-aware-controller` (and `KES_TRACE_CONTAINER=ctrl` if there is no dedicated `tailer` container) before running `fetch` so the script execs the correct pod.

Additional commands include `status` (pod overview), `jobs` (list replay `Job` objects), `logs` (follow the controller deployment), `reset` (tear down namespace + cluster roles), and `helm-{up,down}` to manage the optional Helm stack under `k8s/helm`.

### Policies and deferral knobs
- Toggle policies by setting `SCHEDULER_POLICY` (`ecokube` or `carbonscaler`) on the controller deployment (`kubectl -n workloads set env deploy/ciw-controller SCHEDULER_POLICY=carbonscaler`).
- EcoKube exposes the accelerator fit weight via `ECOKUBE_W_FIT` (default `0.2`) so the cluster and simulator stay in sync with the `--het-w-fit` flag from `cmd/run_sim.go`.
- CarbonScaler honours temporal shifting (`CARBONSCALER_SHIFT_FRACTION`/`DEFAULT_MAX_DEFER_FRACTION`) and resource elasticity (`CARBONSCALER_ELASTICITY`), recording `queue_seconds`, `deferred_for_seconds`, and the chosen `scale` inside `decisions.jsonl`.
- EcoKube reuses the calibrated thesis weights (`α=0.58`, `β=0.21`, `γ=0.21`) and emits the same per-node score traces as the simulator.

The metrics agent exposes pod-level CPU seconds, RSS usage, and process counts for Prometheus. Override `PROM_SIDECAR_*` environment variables in `k8s/replay_workloads.yaml` or the `cluster.sh` script to point at a custom registry.

## Repository layout
```txt
ecokube/
├─ ecokube/              # Scheduler wrapper (Go module)
│  ├─ cmd/                      # Simulator entry-points and helpers
│  ├─ controller/               # Kubernetes controller
│  ├─ pkg/                      # Simulation and shared logic
│  ├─ config/                   # Nodes/sites/workloads CSVs
│  ├─ analysis/sim_results_latest/  # Active simulator artefacts
│  ├─ analysis/k8s_results_latest/  # Latest Kubernetes traces
│  └─ scripts/                  # Sweep and utility scripts
├─ analysis/jupyter/            # Thesis notebooks and helpers
├─ k8s/                         # Manifests and Helm assets
├─ kespolicy/                   # Policy prototypes
├─ analysis/figures/            # Generated figures (sim + k8s)
└─ docs/, examples/             # Supporting material
```
