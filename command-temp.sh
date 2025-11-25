# # HetPolicy (EcoKube)
# POLICY=hetpolicy
# kubectl -n workloads set env deploy/ciw-controller SCHEDULER_POLICY=$POLICY
# yq -i '... set SCHEDULER_LABEL to env(POLICY) ...' k8s/replay_workloads.yaml
# hetsched replay
# hetsched fetch analysis/k8s_results_latest/${POLICY}/decisions.jsonl

# Carbonscaler
POLICY=carbonscaler
kubectl -n workloads set env deploy/ciw-controller SCHEDULER_POLICY=$POLICY
# yq -i '... set SCHEDULER_LABEL to env(POLICY) ...' k8s/replay_workloads.yaml
yq -Yi '(
  select(.kind=="Job")
  | .spec.template.spec.containers[]
  | select(.name=="replayer").env[]
  | select(.name=="SCHEDULER_LABEL").value
) = env.POLICY' k8s/replay_workloads.yaml

hetsched replay
hetsched fetch analysis/k8s_results_latest/${POLICY}/decisions.jsonl

# # Default k8s
# POLICY=k8s
# kubectl -n workloads set env deploy/ciw-controller SCHEDULER_POLICY=$POLICY
# yq -i '... set SCHEDULER_LABEL to env(POLICY) ...' k8s/replay_workloads.yaml
# hetsched replay
# hetsched fetch analysis/k8s_results_latest/${POLICY}/decisions.jsonl

python analysis/scripts/aggregate_k8s.py \
  --het analysis/k8s_results_latest/hetpolicy/decisions.jsonl \
  --carbonscaler analysis/k8s_results_latest/carbonscaler/decisions.jsonl \
  --k8s analysis/k8s_results_latest/k8s-default/decisions.jsonl \
  --workloads kubenergysched/config/workloads.csv \
  --output analysis/k8s_results_quick \
  --figures-dir analysis/figures/k8s_quick