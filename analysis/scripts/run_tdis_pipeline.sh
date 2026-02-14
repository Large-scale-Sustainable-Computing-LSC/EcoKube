#!/usr/bin/env bash
set -euo pipefail

# Deterministic TDIS pipeline (simulator-focused)
# Usage:
#   ./analysis/scripts/run_tdis_pipeline.sh mix-a
#   ./analysis/scripts/run_tdis_pipeline.sh mix-b
#   ./analysis/scripts/run_tdis_pipeline.sh mix-c

MIX="${1:-mix-b}"
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
HETSCHED="$ROOT/hetsched"
TS="$(date +%Y%m%d_%H%M%S)"
OUT_ROOT="$ROOT/analysis/sim_runs/${MIX}_${TS}"
CFG_DIR="$OUT_ROOT/config"
RES_DIR="$OUT_ROOT/results"
LOG="$OUT_ROOT/commands.log"

mkdir -p "$CFG_DIR" "$RES_DIR"

echo "[$(date -Iseconds)] MIX=$MIX" | tee -a "$LOG"

gen_common=(
  go run ./cmd/gen_data.go
  --nodes-out="$CFG_DIR/nodes.csv"
  --workloads-out="$CFG_DIR/workloads.csv"
  --sites-csv-out="$CFG_DIR/sites.csv"
  --sites-json-out="$CFG_DIR/sites.json"
  --seed=20260214
  --jobs=900
)

case "$MIX" in
  mix-a)
    gen_flags=(--arrival-mode=poisson --arrival-rate=1.8 --batch-size=24 --gpu-share=0.05)
    ;;
  mix-b)
    gen_flags=(--arrival-mode=bursty --arrival-rate=0.9 --burst-probability=0.30 --burst-multiplier=3.2 --batch-size=48 --gpu-share=0.12)
    ;;
  mix-c)
    gen_flags=(--arrival-mode=bursty --arrival-rate=1.1 --burst-probability=0.20 --burst-multiplier=2.4 --batch-size=64 --gpu-share=0.28)
    ;;
  *)
    echo "Unknown mix: $MIX" >&2
    exit 1
    ;;
esac

(
  cd "$HETSCHED"
  printf 'CMD: %q ' "${gen_common[@]}" "${gen_flags[@]}" | tee -a "$LOG"; echo | tee -a "$LOG"
  "${gen_common[@]}" "${gen_flags[@]}"

  run_cmd=(
    go run ./cmd/run_sim.go
    --nodes-csv="$CFG_DIR/nodes.csv"
    --wl-csv="$CFG_DIR/workloads.csv"
    --sites-csv="$CFG_DIR/sites.csv"
    --outdir="$RES_DIR"
    --ci-weights=0.2,0.4,0.6,0.8
    --batch-sizes=200,500,900
    --job-counts=300,600,900
    --arrival-rates=0.8,1.1,1.4
    --arrival-mode=bursty
    --arrival-burst-probability=0.25
    --arrival-burst-multiplier=2.5
    --warmup-min=30
    --arrival-seed=20260214
    --het-w-fit=0.2
    --ecokube-modes=weighted-sum
    --trace-jsonl=auto
  )

  printf 'CMD: %q ' "${run_cmd[@]}" | tee -a "$LOG"; echo | tee -a "$LOG"
  "${run_cmd[@]}"
)

echo "Done. Results at: $OUT_ROOT"
