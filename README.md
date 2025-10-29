### 🔋 KubEnergySched - Scheduling Framework for Heterogeneous Multi-Cluster Research Infrastructures
KubEnergySched is the sustainability-aware scheduling framework for Heterogeneous RIs. The goal is to integrate heterogeneous infrastructures while optimising **sustainability** outcomes across simulation and Kubernetes replay tracks. It orchestrates the KesPolicies suite (located under `kespolicy/`) to compare heterogeneous scheduling strategies consistently.

## How to use
- **1. Prepare inputs** – Generate or update `config/nodes.csv`, `config/workloads.csv`, and `config/sites.csv` with `go run ./cmd/gen_data.go` (or reuse the committed defaults).
- **2. Run the simulator sweep** – Execute `./kubenergysched/cmd/sweep_sim.sh`. By default it sweeps `ci_weight ∈ {0.2, 0.4, 0.6, 0.8}` and the thesis batch sizes `{200, 500, 1000}`, writing into `kubenergysched/results_<timestamp>/…`. Symlink or copy the run you want to analyse to `kubenergysched/results_latest`.
- **3. Collect Kubernetes traces (optional)** – Replay the batch via `k8s/replay_workloads.yaml`, then export decisions to `kubenergysched/results_latest/decisions.jsonl`. The simulator notebooks automatically harmonise both sources if the JSONL is present.
- **4. Launch the analysis notebook** – Open `analysis/jupyter/output_capture.ipynb` (or `final_analysis.executed.ipynb`) in Jupyter, run all cells, and review the generated tables, plots, and evaluation metrics.
- **5. Compare policies** – The notebook materialises the carbon and timeliness metrics mandated by the thesis (CFP, SCI, makespan, latency, scheduler overhead, throughput, average energy per job) so both pathways can be contrasted consistently.

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
Use the helper to build consistent node, site, and workload CSVs:
- `cd kubenergysched && go run ./cmd/gen_data.go --nodes-out=config/nodes.csv --workloads-out=config/workloads.csv --sites-csv-out=config/sites.csv --sites-json-out=config/sites.json --seed=42`
  - Omitting `--seed` randomises workloads; keep it to reproduce the thesis dataset.
  - The simulator expects the CSV trio, and the controller consumes the accompanying `sites.json` ConfigMap.

## KesPolicies suite
KubEnergySched evaluates the following KesPolicies implementations:
- **K8sPolicy** – Baseline bin-pack strategy derived from upstream Kubernetes, used as the carbon-unaware reference.
- **KEIDSPolicy** – Weighted composite policy inspired by KEIDS, balancing carbon intensity, runtime, and interference with calibrated weights.
- **TOPSISPolicy** – Technique for Order Preference by Similarity to Ideal Solution, using the same ${α, β, γ}$ weights but ranking nodes via vector normalisation.
- **HetPolicy** – Heterogeneity-aware policy that accounts for node/site diversity while applying the calibrated thesis weights (including optional δ terms).
- **CarbonScalerPolicy** – Replay-only policy mirroring the CarbonScaler controller when Kubernetes trace exports are available for comparison.

## Simulator sweep
- Quick sweep with thesis defaults:
  - `cd kubenergysched && ./cmd/sweep_sim.sh`
- Customise via environment variables before running the script:
  - `SWEEP_CI_WEIGHTS="0.2 0.5 0.8" SWEEP_BATCH_SIZES="200 800" ./cmd/sweep_sim.sh`
  - Additional knobs: `SWEEP_ALPHA_MASS`, `SWEEP_LOOKAHEAD_MIN`, `SWEEP_DUR_SCALE`, `SWEEP_DURATIONS`, `SWEEP_EXTRA_ARGS`.
- Each run emits `summary.csv`, per-policy job CSVs, and the JSONL trace under `kubenergysched/results_<timestamp>/ci_<ci>_bs_<N>/`. After the sweep, mirror the outputs into `kubenergysched/results_latest` (one `*_results.csv` per scheduler/setting plus consolidated `summary.{csv,json}` and `decisions.jsonl`) so the notebooks pick up the latest data.
- Default sweep covers the following policies from KesPolicies:
  - `k8s` (K8sPolicy) – Baseline bin-pack reference.
  - `keids` (KEIDSPolicy) – Weighted sum with thesis-calibrated weights `α=0.58`, `β=0.21`, `γ=0.21`.
  - `topsis` (TOPSISPolicy) – TOPSIS ranking with the same `(α, β, γ)` triple.
  - `hetpolicy` (HetPolicy) – Heterogeneity-aware policy with thesis weights (`α=0.58`, `β=0.21`, `γ=0.21`, `δ=0`).
  - The Kubernetes replay additionally compares `carbonscaler` (CarbonScalerPolicy) against HetPolicy when CarbonScaler traces are available.
- Update or symlink `kubenergysched/results_latest` to point at the run the notebook should consume.

## Kubernetes replay snapshot (optional)
Once a cluster is available, these steps refresh the trace:
1. Build and push controller and workload images (`kubenergysched/controller`, `kubenergysched/workloads`).
2. Reset the `workloads` namespace, load the generated CSVs as ConfigMaps, and label nodes (site `B` by default).
3. Deploy `k8s/manifests/ciw-controller.yaml`, switch to the debug image if interactive access is needed, and apply `k8s/replay_workloads.yaml`.
4. When jobs have completed, export the trace:  
   `kubectl -n workloads exec deploy/ciw-controller -- cat /var/log/ciw/decisions.jsonl > kubenergysched/results_latest/decisions.jsonl`
5. Rerun the notebook to evaluate Kubernetes against the simulator sweeps.

## Repository layout
```txt
kestack/
├─ kubenergysched/              # Scheduler wrapper (Go module)
│  ├─ cmd/                      # Simulator entry-points and helpers
│  ├─ controller/               # Kubernetes controller
│  ├─ pkg/                      # Simulation and shared logic
│  ├─ config/                   # Nodes/sites/workloads CSVs
│  ├─ results_latest/           # Active simulator + replay artefacts
│  └─ scripts/                  # Sweep and utility scripts
├─ analysis/jupyter/            # Thesis notebooks and helpers
├─ k8s/                         # Manifests and Helm assets
├─ kespolicy/                   # Policy prototypes
├─ sim/                         # Power trace tooling
└─ docs/, assets/, examples/    # Supporting material
```
