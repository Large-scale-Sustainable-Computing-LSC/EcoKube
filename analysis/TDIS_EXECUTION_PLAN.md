# TDIS Execution Plan (Deterministic + Reproducible + IoT-focused)

## 1) Reproducibility contract

- Pin all seeds and run knobs in scripts (no ad-hoc manual runs).
- Keep one run root per experiment campaign: `analysis/sim_runs/<timestamp>/`.
- Store generated inputs (`nodes.csv`, `workloads.csv`, `sites.csv`, `sites.json`) inside each run root.
- Store run metadata (`params.json`) and command log (`commands.log`).
- Keep simulator and plot environments stable (`python -m venv` + requirements file + pinned matplotlib/seaborn versions if needed).

---

## 2) IoT workload mixes (generator presets)

All mixes use deterministic generation (`--seed`) and controlled arrivals.

### Mix A — Periodic sensing (steady, lightweight)
- Target: telemetry/sensor ingestion with low per-job demand.
- Flags:
  - `--arrival-mode=poisson`
  - `--arrival-rate=1.8`
  - `--batch-size=24`
  - `--gpu-share=0.05`

### Mix B — Event-driven edge bursts (bursty)
- Target: anomaly/spike windows, backpressure behavior.
- Flags:
  - `--arrival-mode=bursty`
  - `--arrival-rate=0.9`
  - `--burst-probability=0.30`
  - `--burst-multiplier=3.2`
  - `--batch-size=48`
  - `--gpu-share=0.12`

### Mix C — Mixed edge AI + control (heterogeneous)
- Target: mixed CPU/GPU jobs with stronger heterogeneity pressure.
- Flags:
  - `--arrival-mode=bursty`
  - `--arrival-rate=1.1`
  - `--burst-probability=0.20`
  - `--burst-multiplier=2.4`
  - `--batch-size=64`
  - `--gpu-share=0.28`

---

## 3) Deterministic simulator sweep baseline

Use one fixed arrival seed and fixed repetitions in code path (already deterministic per scenario seed logic in `run_sim.go`).

Recommended sweep knobs:
- `--ci-weights=0.2,0.4,0.6,0.8`
- `--job-counts=300,600,900`
- `--arrival-rates=0.8,1.1,1.4`
- `--warmup-min=30`
- `--arrival-seed=20260214`
- `--het-w-fit=0.2`

Policy set: keep EcoKube + strongest baselines (`k8s`, `keids`, `topsis`, optional carbonscaler).

---

## 4) Publication-grade figures

- Use consistent style file (`analysis/scripts/paper_style.mplstyle`).
- Export both PNG (300 dpi) and PDF (vector) for Overleaf.
- Keep visual narrative to 2–3 key figures:
  1. Pareto frontier (energy vs carbon)
  2. Robustness across workload mixes (delta bars/boxen)
  3. Optional latency tail violin (if space allows)
- Add effect-size annotation in captions (not only p-values).

---

## 5) Candidate real-world traces for IoT-inspired calibration

- Alibaba cluster trace (production-scale job traces):
  - https://github.com/alibaba/clusterdata
- Google cluster traces (Borg):
  - https://github.com/google/cluster-data

Use these traces to calibrate:
- inter-arrival distribution,
- runtime heavy-tail,
- burst characteristics,
- workload class proportions.

(If needed later: add a lightweight trace-to-generator adapter script under `analysis/scripts/`.)

---

## 6) Immediate execution checklist

1. Generate deterministic inputs per mix.
2. Run one smoke test per mix.
3. Run full sweeps for selected mixes.
4. Generate figures/tables.
5. Curate final 2–3 figures for Overleaf.
6. Update Methods + Results text to claim-evidence format.
