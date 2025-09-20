## Phase 1
### 1.1 Create note labels.
```bash
kubectl label node worker-a site=A
kubectl label node worker-b site=B
kubectl label node worker-c site=C
```

1.1.2 ConfigMap (per-site parameters)
```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: site-params
  namespace: ci-aware
data:
  sites.json: |
    {
      "A": {"pue": 1.18, "k": 0.37, "region": "NL"},
      "B": {"pue": 1.05, "k": 0.22, "region": "ON"},
      "C": {"pue": 1.60, "k": 0.45, "region": "CA"}
    }
```

### 1.2 Metrics pipeline
```yaml
- job_name: 'scaphandre'
  kubernetes_sd_configs: [{role: pod}]
  relabel_configs:
    - action: replace
      source_labels: [__meta_kubernetes_pod_node_name]
      target_label: node
    - action: replace
      source_labels: [__meta_kubernetes_node_label_site]
      target_label: site
```

### 1.3 Forecast service
```go
ci_current_g_per_kwh{site="A"}  410
ci_current_g_per_kwh{site="B"}  120
ci_current_g_per_kwh{site="C"}  520
```

### 1.4 CI-Aware Scheduler component (minimal for the moment)
Goal: Patch incoming Jobs+Pods with a `nodeAffinity` for the chosen `site_id`.

The object patch:
```yaml
spec:
  affinity:
    nodeAffinity:
      requiredDuringSchedulingIgnoredDuringExecution:
        nodeSelectorTerms:
        - matchExpressions:
          - key: site
            operator: In
            values: ["B"]    # the chosen site by the affinity
```
Go scaffold (`controller/main.go`): just the essentials:
```go
package main

func main() {
  // 1) Load site params (from ConfigMap mounted file)
  // 2) Init Prometheus client (ForecastService + Scaphandre queries)
  // 3) Init K8s client + informer for Pods/Jobs in target namespaces
  // 4) On Add, call DecideSite(jobSpec) -> siteID
  // 5) Patch the object with nodeAffinity (site)
}
```
Scoring wrapper (`controller/pkg/scoring/score.go`):
```go
type SiteSignal struct {
  SiteID string
  PUE    float64
  K      float64          // facility factor from your model
  CIg    float64          // ci_current_g_per_kwh from ForecastService
  PowerW float64          // optional: from Scaphandre aggregate per site
}

type JobSpecLite struct {
  Cpus int
  MemGB int
  DurSec int
  SubmitTs time.Time
}

func ScoreSites(job JobSpecLite, sites []SiteSignal, params CISchedParams) (best string, ranks map[string]float64) {
  // Map SiteSignal -> inputs expected by models/cisched
  // Call cisched scoring (AlphaMass, Lookahead)
  // Return argmin (your cost) or argmax (your utility)
  return
}
```
Prometheus integration (`controller/pkg/prom/queries.go`)
- Carbon Intensity (instant): `ci_current_g_per_kwh{site=~".+"}`
- Optional: power/energy baselines to refine placement.
    - Average node power (last 5-10 minutes): `avg_over_time(scaph_node_power_watts{site="$SITE_ID"}[10m])`
    - CPU util hints: `avg(node_cpu_seconds_total{mode!="idle",site="$SITE_ID"})`

### 1.5 Workload submission (trace replayer)
Render `workloads.csv` -> Job manifests (or a single controller that submits Job objects at `submit_time` with the requested CPU/mem/duration).

For HPC-like tasks with Slurm semantics, you can wrap inside a container that runs an MPI benchmark or ML train loop with a `sleep`/CPU-burner + telemetry labels for the MVP.

## Phase 2 implementation
### 2.1 Bring up Karmada + join 2-3 member clusters
### 2.2 Cross-cluster metrics
### 2.3 Placement: cluster-level, node-level afterwards
Not sure if this is the best approach. Empirically, one thing that I believe that it is going to be a threat to application is: sometime the "greenest" site cannot be always taken. Because:
- Some CI's make some sites unusally greener, therefore they're going to be always picked.
- CI and other KPIs are abstractions; this is a multi-objective optimisation, so perhaps having the "greenest" job is not always the first priority.
### 2.4 Replay workloads to/in Karmada

## 3 KPIs & Evaluation Alignment (with the simulation)
### 3.1 Job Labelling & Logs
### 3.2 Prometheus Series to store events
### 3.3 KPIs formulas
### 3.4 Export scripts
This is probably going to be quick and dirty. :D

# Federated testbed implementation notes
Here are the commands and steps to recreate the steps mentioned before.

### Chapter 1: Multi-cluster in 1 node.
```sh
# Create namespace and add sites.json
kubectl apply -f helm/charts/cluster_testbed/templates/site-config-configmap.yaml
# Spin up deployment and service for the Forecast module.
kubectl apply -f helm/charts/cluster_testbed/templates/forecast-service.yaml

# Create monitoring namespace and apply the prom stack from Helm:
helm upgrade --install prom prometheus-community/kube-prometheus-stack \
  -n monitoring --create-namespace \
  -f helm/charts/prometheus/values.yaml

# 4.1 Build images
export REG=docker.io/goncaloferreirauva
export TAG=0.1
./scripts/build_push_image.sh

# 4.3 Label nodes to emulate sites (once)
kubectl label node <worker-a> site=A --overwrite
kubectl label node <worker-b> site=B --overwrite
kubectl label node <worker-c> site=C --overwrite



# 4.4 Apply site params + forecast service (reuse files)
kubectl apply -f helm/charts/cluster_testbed/site-config-configmap.yaml
kubectl apply -f helm/charts/cluster_testbed/forecast-service.yaml

# 4.5 Prometheus stack
helm upgrade --install prom prometheus-community/kube-prometheus-stack \
  -n monitoring --create-namespace -f helm/prometheus/values.yaml

# 4.6 Deploy controller + replayer via chart
helm upgrade --install cluter-testbed ./helm/charts/cluster_testbed \
  --set ciAware.image=$REG/ci-aware-controller:0.1 \
  --set replayer.image=$REG/workload-replayer:0.1

```

**Folders to review/delete:**
- helm
  - helm/templates
  - helm/charts -> what's this
  - helm/data -> what is this used for?
  - helm/manual_config -> crd's and other podMonitor configs for Prometheus. Not sure if it is working.


```bash
# Create the site-params config in both workloads and ci-aware namespaces:
kubectl -n ci-aware get configmap site-params -o yaml \
| sed 's/namespace: ci-aware/namespace: workloads/' \
| kubectl apply -f -

kubectl -n workloads patch job workloads-replayer --type='json' -p='[
  {"op":"replace","path":"/spec/template/spec/containers/0/image","value":"goncaloferreirauva/workload-replayer:0.1"},
  {"op":"remove","path":"/spec/template/spec/containers/0/volumeMounts/0","value":{}}
]' || true

# delete and re-create the replayer Job via Helm so it picks up the right template
kubectl -n workloads delete job workloads-replayer --ignore-not-found
helm upgrade --install cluster-testbed ./helm/charts/cluster_testbed \
  --set ciAware.image=goncaloferreirauva/ci-aware-controller:0.1 \
  --set replayer.image=goncaloferreirauva/workload-replayer:0.1

kubectl -n workloads rollout restart deploy/ci-aware-controller

# Clean jobs
kubectl -n workloads delete job -l ciw/scheduler=baseline

## Clean jobs force, quicker
# stop anything else submitting
kubectl -n workloads delete job workloads-replayer --ignore-not-found

# delete all Jobs quickly (no grace, don’t wait)
kubectl -n workloads delete job --all --force --grace-period=0 --wait=false

# also clear Pods (speeds things up)
kubectl -n workloads delete pod -l job-name --force --grace-period=0 --wait=false

# Sanity checks:
# controller healthy
kubectl -n workloads get pods
kubectl -n workloads logs -f deploy/ci-aware-controller

# replayer submits Jobs
kubectl -n workloads logs -f job/workloads-replayer
kubectl -n workloads get jobs,pods -o wide

# confirm affinity injected
kubectl -n workloads describe job <one-job> | sed -n '/Affinity/,+8p'
```