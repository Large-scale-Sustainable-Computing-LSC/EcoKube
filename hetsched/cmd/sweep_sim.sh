#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: sweep_sim.sh [options]

Options:
  --target-folder DIR   Sync an existing sweep directory into analysis/results and exit.
  -h, --help            Show this help message.

Environment variables (when running a sweep):
  SWEEP_CI_WEIGHTS      Space-separated CI weights to sweep.
  SWEEP_BATCH_SIZES     Space-separated batch sizes to sweep.
  SWEEP_OUT_PREFIX      Output directory prefix (relative to hetsched/ unless absolute).
  SWEEP_SYNC_DEST       Destination directory for synced artefacts (default: analysis/results).
EOF
}

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
MODULE_DIR="${REPO_ROOT}/hetsched"

if [[ ! -d "${MODULE_DIR}" ]]; then
  echo "error: expected hetsched module at ${MODULE_DIR}" >&2
  exit 1
fi

DEFAULT_SYNC_DEST="${REPO_ROOT}/analysis/results"
COPY_DEST="${SWEEP_SYNC_DEST:-${DEFAULT_SYNC_DEST}}"
RUN_SWEEP="true"
MANUAL_COPY_SOURCE=""

copy_results() {
  local source_input="${1:-}"
  local dest_input="${2:-${DEFAULT_SYNC_DEST}}"

  if [[ -z "${source_input}" ]]; then
    echo "error: copy_results requires a source directory" >&2
    return 1
  fi

  local resolved_source="${source_input}"
  if [[ ! -d "${resolved_source}" ]]; then
    if [[ "${resolved_source}" != /* ]] && [[ -d "${MODULE_DIR}/${resolved_source}" ]]; then
      resolved_source="${MODULE_DIR}/${resolved_source}"
    else
      echo "error: source directory ${source_input} not found" >&2
      return 1
    fi
  fi

  if ! command -v python3 >/dev/null 2>&1; then
    echo "error: python3 command not found; required for results sync" >&2
    return 1
  fi

  local source_dir
  if ! source_dir="$(cd "${resolved_source}" 2>/dev/null && pwd)"; then
    echo "error: failed to resolve source directory: ${resolved_source}" >&2
    return 1
  fi

  local dest_dir="${dest_input}"
  if [[ "${dest_dir}" != /* ]]; then
    if [[ "${dest_dir}" == hetsched/* ]]; then
      dest_dir="${REPO_ROOT}/${dest_dir}"
    elif [[ "${dest_dir}" == analysis/* ]]; then
      dest_dir="${REPO_ROOT}/${dest_dir}"
    else
      dest_dir="${MODULE_DIR}/${dest_dir}"
    fi
  fi
  local dest_parent
  if ! dest_parent="$(cd "$(dirname "${dest_dir}")" 2>/dev/null && pwd)"; then
    echo "error: failed to resolve destination parent for ${dest_dir}" >&2
    return 1
  fi
  dest_dir="${dest_parent}/$(basename "${dest_dir}")"

  local tmp_dir
  if ! tmp_dir="$(mktemp -d 2>/dev/null)"; then
    if ! tmp_dir="$(mktemp -d -t sweep_sync.XXXXXX)"; then
      echo "error: failed to create temporary directory for results sync" >&2
      return 1
    fi
  fi

  if ! python3 - "${source_dir}" "${tmp_dir}" <<'PY'; then
import csv
import json
import shutil
import sys
from pathlib import Path

source = Path(sys.argv[1])
dest = Path(sys.argv[2])
dest.mkdir(parents=True, exist_ok=True)

summary_files = sorted(path for path in source.glob("**/summary.csv") if path.is_file())
if not summary_files:
    print(f"error: no summary.csv files found under {source}", file=sys.stderr)
    sys.exit(1)

rows = []
fieldnames = None
for summary_path in summary_files:
    with summary_path.open("r", newline="") as handle:
        reader = csv.DictReader(handle)
        if reader.fieldnames is None:
            continue
        if fieldnames is None:
            fieldnames = reader.fieldnames
        elif reader.fieldnames != fieldnames:
            print(f"error: inconsistent summary headers in {summary_path}", file=sys.stderr)
            sys.exit(1)
        for row in reader:
            rows.append(dict(row))

if fieldnames is None or not rows:
    print(f"error: no rows found in summary.csv files under {source}", file=sys.stderr)
    sys.exit(1)

summary_csv = dest / "summary.csv"
with summary_csv.open("w", newline="") as handle:
    writer = csv.DictWriter(handle, fieldnames=fieldnames)
    writer.writeheader()
    writer.writerows(rows)

def coerce(value):
    value = value.strip()
    if not value:
        return value
    try:
        return int(value)
    except ValueError:
        try:
            return float(value)
        except ValueError:
            return value

json_rows = [{key: coerce(value) for key, value in row.items()} for row in rows]
with (dest / "summary.json").open("w", encoding="utf-8") as handle:
    json.dump(json_rows, handle, indent=2)

for results_file in sorted(path for path in source.glob("**/*_results.csv") if path.is_file()):
    shutil.copy2(results_file, dest / results_file.name)

decision_files = sorted(path for path in source.glob("**/decisions.jsonl") if path.is_file())
if decision_files:
    out_path = dest / "decisions.jsonl"
    with out_path.open("w", encoding="utf-8") as outfile:
        for dec_path in decision_files:
            with dec_path.open("r", encoding="utf-8") as infile:
                for line in infile:
                    outfile.write(line.rstrip("\n") + "\n")
PY
    local status=$?
    rm -rf "${tmp_dir}"
    return "${status}"
  fi

  mkdir -p "${dest_parent}"
  rm -rf "${dest_dir}"
  if ! mv "${tmp_dir}" "${dest_dir}"; then
    echo "error: failed to move synced results into ${dest_dir}" >&2
    rm -rf "${tmp_dir}"
    return 1
  fi

  echo "Synced results from ${source_dir} to ${dest_dir}"
  return 0
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --target-folder=*)
      MANUAL_COPY_SOURCE="${1#*=}"
      RUN_SWEEP="false"
      shift
      ;;
    --target-folder)
      shift
      if [[ $# -eq 0 ]]; then
        echo "error: --target-folder requires a directory path" >&2
        exit 1
      fi
      MANUAL_COPY_SOURCE="$1"
      RUN_SWEEP="false"
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "error: unknown argument: $1" >&2
      usage
      exit 1
      ;;
  esac
done

if [[ "${RUN_SWEEP}" == "false" ]]; then
  if ! copy_results "${MANUAL_COPY_SOURCE}" "${COPY_DEST}"; then
    exit 1
  fi
  exit 0
fi

if ! command -v go >/dev/null 2>&1; then
  echo "error: go command not found in PATH" >&2
  exit 1
fi

CI_WEIGHTS=()
if [[ -n "${SWEEP_CI_WEIGHTS:-}" ]]; then
  read -r -a CI_WEIGHTS <<< "${SWEEP_CI_WEIGHTS}"
else
  CI_WEIGHTS=(0.2 0.4 0.6 0.8)
fi

BATCH_SIZES=()
if [[ -n "${SWEEP_BATCH_SIZES:-}" ]]; then
  read -r -a BATCH_SIZES <<< "${SWEEP_BATCH_SIZES}"
else
  BATCH_SIZES=(200 500 1000)
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

if ! copy_results "${RUN_BASE_ABS}" "${COPY_DEST}"; then
  exit 1
fi
