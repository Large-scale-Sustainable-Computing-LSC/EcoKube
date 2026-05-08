RUN=analysis/sim_runs/ce_rerun_20260218_215315_safe

python3 - <<'PY'
import pandas as pd
from pathlib import Path
run = Path("analysis/sim_runs/ce_rerun_20260218_215315_safe")
a = pd.read_csv(run/"chunk_ar0p8"/"summary.csv")
b = pd.read_csv(run/"chunk_ar1p1"/"summary.csv")
pd.concat([a,b], ignore_index=True).to_csv(run/"combined_partial_summary.csv", index=False)
print("Wrote", run/"combined_partial_summary.csv")
PY

python3 analysis/scripts/generate_partial_figures.py \
  --summary "$RUN/combined_partial_summary.csv" \
  --out-dir "test/figures_partial"
  --style analysis/scripts/paper_style.mplstyle