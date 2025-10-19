### 🔋 THEMISTACK · Hermes
Hermes is the sustainability-aware scheduler wrapper inside THEMISTACK. The goal is to integrate heterogeneous cloud infrastructures while optimising **sustainability**.

- **Fully-managed**: the user (developer/researcher) does not have to worry about the underlying computation and resource allocation.
- **Kubernetes-based**: Kubernetes is the *de facto* cluster framework used at the core of many cloud infrastructures.

### TODO development
- `scoreOnJobNode` and `SelectSiteAndNode` on `scheduler.go`: it's commented out, needs to be implemented.
- (Optional): clean unused files + Docker testbed configs.

### Testbed Architecture (WIP)
![Testbed Architecture](assets/testbed_architecture.png)

### Repository layout
```txt
themistack/
├─ KubEnergySched/              # Scheduler wrapper (Go module)
│  ├─ cmd/run_sim.go
│  ├─ cmd/gen_data.go           # CSV/JSON data generator (nodes/sites/workloads)
│  ├─ controller/               # K8s controller (Go module)
│  ├─ pkg/                      # Simulation core + shared structs
│  ├─ config/                   # CSV inputs (nodes/sites/workloads)
│  ├─ scripts/                  # Helper scripts
│  ├─ results/                  # Simulation outputs
│  └─ workloads/                # Generated workloads
├─ themis/
│  └─ policies/                 # Sustainability policies (former models)
├─ sim/
│  └─ powertrace/               # Trace tooling and features
├─ k8s/
│  └─ helm/                     # Helm charts and manifests
├─ kpis/
│  └─ forecast_service/         # Forecast / KPI microservice stub
├─ examples/
│  ├─ fabric_testbed/           # FABRIC automation scripts and notes
│  └─ jupyter/                  # Analysis notebooks
└─ docs/
   ├─ PLAN.md                   # Project refactor plan
   ├─ assets/                   # Architecture diagrams
   └─ thesis-overleaf/          # Thesis sources
```

### Generate CSV/JSON
- Recommended one‑liner (generates nodes.csv, workloads.csv, sites.csv, and sites.json):
  - `cd hermes && go run ./cmd/gen_data.go --nodes-out=config/nodes.csv --workloads-out=config/workloads.csv --sites-csv-out=config/sites.csv --sites-json-out=config/sites.json --seed=42`

- Individual outputs, if needed:
  - Nodes CSV: `cd hermes && go run ./cmd/gen_data.go --nodes-out=config/nodes.csv`
  - Workloads CSV: `cd hermes && go run ./cmd/gen_data.go --workloads-out=config/workloads.csv --seed=42`
  - Sites CSV (simulator): `cd hermes && go run ./cmd/gen_data.go --sites-csv-out=config/sites.csv`
  - Sites JSON (controller/helm): `cd hermes && go run ./cmd/gen_data.go --sites-json-out=sites.json`

Notes
- Simulator expects `config/nodes.csv`, `config/workloads.csv`, `config/sites.csv`.
- K8s controller expects a `sites.json` ConfigMap (see `k8s/helm/charts/cluster_testbed/templates/site-config-configmap.yaml`).

