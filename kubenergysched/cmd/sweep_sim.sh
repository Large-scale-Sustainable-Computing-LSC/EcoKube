#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
MODULE_DIR="${REPO_ROOT}/kubenergysched"

if [[ ! -d "${MODULE_DIR}" ]]; then
  echo "error: expected kubenergysched module at ${MODULE_DIR}" >&2
  exit 1
fi

if ! command -v go >/dev/null 2>&1; then
  echo "error: go command not found in PATH" >&2
  exit 1
fi

CI_WEIGHTS=()
if [[ -n "${SWEEP_CI_WEIGHTS:-}" ]]; then
  read -r -a CI_WEIGHTS <<< "${SWEEP_CI_WEIGHTS}"
else
  CI_WEIGHTS=(0.05 0.2 0.8 1.2)
fi

BATCH_SIZES=()
if [[ -n "${SWEEP_BATCH_SIZES:-}" ]]; then
  read -r -a BATCH_SIZES <<< "${SWEEP_BATCH_SIZES}"
else
  BATCH_SIZES=(32 128 256)
fi

ALPHA_MASS="${SWEEP_ALPHA_MASS:-1.0}"
LOOKAHEAD_MIN="${SWEEP_LOOKAHEAD_MIN:-0}"
DUR_SCALE="${SWEEP_DUR_SCALE:-1.0}"
DURATIONS="${SWEEP_DURATIONS:-}"
NODES_CSV="${SWEEP_NODES_CSV:-config/nodes.csv}"
WORKLOAD_CSV="${SWEEP_WORKLOAD_CSV:-config/workloads.csv}"

EXTRA_ARGS=()
if [[ -n "${SWEEP_EXTRA_ARGS:-}" ]]; then
  read -r -a EXTRA_ARGS <<< "${SWEEP_EXTRA_ARGS}"
fi

timestamp="$(date +%Y%m%d_%H%M%S)"
OUT_PREFIX_RAW="${SWEEP_OUT_PREFIX:-results}"
OUT_PREFIX_TRIM="${OUT_PREFIX_RAW%/}"
if [[ "${OUT_PREFIX_TRIM}" = /* ]]; then
  OUT_PREFIX_ABS="${OUT_PREFIX_TRIM}"
  RUN_BASE_ARG="${OUT_PREFIX_ABS}/results_${timestamp}"
else
  OUT_PREFIX_REL="${OUT_PREFIX_TRIM}"
  OUT_PREFIX_ABS="${MODULE_DIR}/${OUT_PREFIX_REL}"
  RUN_BASE_ARG="${OUT_PREFIX_REL}/results_${timestamp}"
fi
RUN_BASE_ABS="${OUT_PREFIX_ABS}/results_${timestamp}"
mkdir -p "${RUN_BASE_ABS}"

echo "Sweep output root: ${RUN_BASE_ABS}"

for ci in "${CI_WEIGHTS[@]}"; do
  for bs in "${BATCH_SIZES[@]}"; do
    ci_slug="${ci//./p}"
    ci_slug="${ci_slug//-/m}"
    run_dir_abs="${RUN_BASE_ABS}/ci_${ci_slug}_bs_${bs}"
    mkdir -p "${run_dir_abs}"
    if [[ "${OUT_PREFIX_TRIM}" = /* ]]; then
      run_dir_arg="${run_dir_abs}"
      trace_file_arg="${run_dir_abs}/decisions.jsonl"
    else
      run_dir_arg="${RUN_BASE_ARG}/ci_${ci_slug}_bs_${bs}"
      trace_file_arg="${run_dir_arg}/decisions.jsonl"
    fi

    cat <<EOF > "${run_dir_abs}/params.json"
{
  "ci_weight": "${ci}",
  "batch_size": "${bs}",
  "alpha_mass": "${ALPHA_MASS}",
  "lookahead_min": "${LOOKAHEAD_MIN}",
  "dur_scale": "${DUR_SCALE}",
  "durations": "${DURATIONS}",
  "nodes_csv": "${NODES_CSV}",
  "workloads_csv": "${WORKLOAD_CSV}",
  "timestamp": "$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
}
EOF

    cmd=(go run ./cmd/run_sim.go
      --nodes-csv="${NODES_CSV}"
      --wl-csv="${WORKLOAD_CSV}"
      --ci-weights="${ci}"
      --batch-sizes="${bs}"
      --alpha-mass="${ALPHA_MASS}"
      --lookahead-min="${LOOKAHEAD_MIN}"
      --dur-scale="${DUR_SCALE}"
      --outdir="${run_dir_arg}"
      --trace-jsonl="${trace_file_arg}"
    )

    if [[ -n "${DURATIONS}" ]]; then
      cmd+=(--durations="${DURATIONS}")
    fi
    if ((${#EXTRA_ARGS[@]})); then
      cmd+=("${EXTRA_ARGS[@]}")
    fi

    echo "→ ci_weight=${ci} batch_size=${bs}"
    (
      cd "${MODULE_DIR}"
      "${cmd[@]}"
    )
  done
done

echo "Sweep finished. Results in ${RUN_BASE_ABS}"
