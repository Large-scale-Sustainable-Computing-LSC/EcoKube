#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
NAMESPACE="${WORKLOAD_NS:-workloads}"
HELM_RELEASE="${KES_HELM_RELEASE:-kes-replay}"
HELM_NAMESPACE="${KES_HELM_NAMESPACE:-$NAMESPACE}"
CONTROLLER_DEPLOYMENT="${KES_CONTROLLER_DEPLOYMENT:-ciw-controller}"
CHART_DIR="$REPO_ROOT/k8s/helm"
MANIFEST_DIR="$REPO_ROOT/k8s/manifests"
CONFIG_DIR="$REPO_ROOT/kubenergysched/config"
RESULT_DIR="${RESULT_DIR:-$REPO_ROOT/kubenergysched/results_latest_k8s}"

usage() {
  cat <<USAGE
Usage: $(basename "$0") <command>

Commands:
  bootstrap        Prepare namespace, configmaps, and deploy the controller
  replay           Launch the workload replayer job
  fetch [path]     Copy decisions.jsonl from the controller pod (default: $RESULT_DIR/decisions.jsonl)
  status           Show pod status in the workloads namespace
  logs             Stream controller logs
  reset            Delete controller resources, namespace, and Helm releases
  helm-up          Install/upgrade the Helm stack in $HELM_NAMESPACE
  helm-down        Uninstall the Helm stack in $HELM_NAMESPACE
USAGE
}

ensure_namespace() {
  echo ">>> Ensuring namespace $NAMESPACE exists"
  kubectl create namespace "$NAMESPACE" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
}

apply_configmaps() {
  ensure_namespace
  echo ">>> Updating workload ConfigMaps"
  kubectl -n "$NAMESPACE" create configmap workloads-csv \
    --from-file=workloads.csv="$CONFIG_DIR/workloads.csv" \
    --dry-run=client -o yaml | kubectl apply -f -
  kubectl -n "$NAMESPACE" create configmap nodes-csv \
    --from-file=nodes.csv="$CONFIG_DIR/nodes.csv" \
    --dry-run=client -o yaml | kubectl apply -f -
  kubectl -n "$NAMESPACE" create configmap ciw-sites \
    --from-file=sites.json="$CONFIG_DIR/sites.json" \
    --dry-run=client -o yaml | kubectl apply -f -
}

bootstrap() {
  apply_configmaps
  echo ">>> Deploying CI-aware controller"
  kubectl apply -f "$MANIFEST_DIR/ciw-controller.yaml"
}

replay() {
  apply_configmaps
  echo ">>> Launching workload replayer"
  kubectl apply -f "$REPO_ROOT/k8s/replay_workloads.yaml"
  echo ">>> Waiting for replay job to complete"
  kubectl -n "$NAMESPACE" wait --for=condition=complete job/workloads-replayer --timeout=15m
}

fetch() {
  out="${1:-$RESULT_DIR/decisions.jsonl}"
  mkdir -p "$(dirname "$out")"
  echo ">>> Exporting decisions to $out"
  echo ">>> Namespace is $NAMESPACE"
  kubectl -n "$NAMESPACE" exec deploy/"$CONTROLLER_DEPLOYMENT" -c tailer -- cat /var/log/ciw/decisions.jsonl > "$out"
}

status() {
  kubectl -n "$NAMESPACE" get pods
}

logs() {
  kubectl -n "$NAMESPACE" logs deploy/ciw-controller -f
}

reset() {
  echo ">>> Tearing down controller resources"
  kubectl delete -f "$MANIFEST_DIR/ciw-controller.yaml" --ignore-not-found
  echo ">>> Removing namespace $NAMESPACE"
  kubectl delete namespace "$NAMESPACE" --ignore-not-found
  echo ">>> Removing Helm releases"
  helm uninstall "$HELM_RELEASE" -n "$HELM_NAMESPACE" --ignore-not-found >/dev/null || true
}

helm_up() {
  ensure_namespace
  echo ">>> Installing Helm release $HELM_RELEASE in namespace $HELM_NAMESPACE"
  helm upgrade --install "$HELM_RELEASE" "$CHART_DIR" -n "$HELM_NAMESPACE" --create-namespace
}

helm_down() {
  echo ">>> Uninstalling Helm release $HELM_RELEASE"
  helm uninstall "$HELM_RELEASE" -n "$HELM_NAMESPACE" --ignore-not-found
}

cmd="${1:-help}"
shift || true

case "$cmd" in
  bootstrap) bootstrap ;;
  replay) replay ;;
  fetch) fetch "$@" ;;
  status) status ;;
  logs) logs ;;
  reset) reset ;;
  helm-up) helm_up ;;
  helm-down) helm_down ;;
  help|--help|-h) usage ;;
  *) echo "Unknown command: $cmd" >&2; usage; exit 1 ;;
esac
