#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
HERMES_DIR="${REPO_ROOT}/hermes"

if [[ ! -d "${HERMES_DIR}" ]]; then
  echo "error: expected hermes module at ${HERMES_DIR}" >&2
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

OUT_PREFIX="${SWEEP_OUT_PREFIX:-${REPO_ROOT}/analysis/results}"
mkdir -p "${OUT_PREFIX}"

timestamp="$(date +%Y%m%d_%H%M%S)"
RUN_BASE="${OUT_PREFIX}/sweep_${timestamp}"
mkdir -p "${RUN_BASE}"

echo "Sweep output root: ${RUN_BASE}"

for ci in "${CI_WEIGHTS[@]}"; do
  for bs in "${BATCH_SIZES[@]}"; do
    ci_slug="${ci//./p}"
    ci_slug="${ci_slug//-/m}"
    run_dir="${RUN_BASE}/ci_${ci_slug}_bs_${bs}"
    mkdir -p "${run_dir}"

    trace_file="${run_dir}/decisions.jsonl"

    cat <<EOF > "${run_dir}/params.json"
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
      --outdir="${run_dir}"
      --trace-jsonl="${trace_file}"
    )

    if [[ -n "${DURATIONS}" ]]; then
      cmd+=(--durations="${DURATIONS}")
    fi
    if ((${#EXTRA_ARGS[@]})); then
      cmd+=("${EXTRA_ARGS[@]}")
    fi

    echo "→ ci_weight=${ci} batch_size=${bs}"
    (
      cd "${HERMES_DIR}"
      "${cmd[@]}"
    )
  done
done

echo "Sweep finished. Results in ${RUN_BASE}"
