#!/usr/bin/env python3
"""
Generate LaTeX tables for the Kubernetes replay results.

This mirrors ``generate_sim_tables.py`` but operates on ``analysis/k8s_results``
or any directory laid out as ``batch_<size>/summary.csv``.
"""

from __future__ import annotations

import argparse
from dataclasses import dataclass
from pathlib import Path
from typing import Iterable, List

import numpy as np
import pandas as pd

POLICY_LABELS = {
    "hetpolicy": "HetPolicy",
    "carbonscaler": "CarbonScaler",
}

DEFAULT_BATCHES = (200, 500, 1000)


@dataclass
class TableResults:
    per_batch: pd.DataFrame
    overall: pd.DataFrame


def _load_summary(path: Path) -> pd.DataFrame:
    if not path.exists():
        raise FileNotFoundError(path)
    df = pd.read_csv(path)
    df.columns = [c.strip().lower() for c in df.columns]
    df.rename(
        columns={
            "policy": "policy",
            "total_ci_cost_g": "total_ci_cost_g",
            "avg_wait_s": "avg_wait_s",
            "makespan_s": "makespan_s",
        },
        inplace=True,
    )
    return df


def _compute_pareto(df: pd.DataFrame) -> pd.Series:
    values = df[["total_ci_cost_g", "makespan_s"]].to_numpy(dtype=float)
    dominated = np.zeros(len(values), dtype=bool)
    for i, point in enumerate(values):
        mask = np.ones(len(values), dtype=bool)
        mask[i] = False
        better = (values[mask] <= point).all(axis=1) & (values[mask] < point).any(axis=1)
        if better.any():
            dominated[i] = True
    return pd.Series(~dominated, index=df["policy"])


def build_k8s_tables(results_root: Path, batches: Iterable[int] = DEFAULT_BATCHES) -> TableResults:
    per_batch_rows: List[dict] = []

    for batch in batches:
        summary_path = results_root / f"batch_{batch}" / "summary.csv"
        df = _load_summary(summary_path)
        df = df[df["policy"].isin(POLICY_LABELS.keys())].copy()
        if df.empty:
            continue
        pareto = _compute_pareto(df)
        for _, row in df.iterrows():
            label = POLICY_LABELS.get(row["policy"], row["policy"])
            per_batch_rows.append(
                {
                    "Policy": label,
                    "Batch": batch,
                    "CI Weight": row.get("ci_weight", 0.20),
                    "Total CFP [gCO2e]": row["total_ci_cost_g"],
                    "Avg Wait [s]": row["avg_wait_s"],
                    "Makespan [s]": row["makespan_s"],
                    "Pareto Front": bool(pareto[row["policy"]]),
                }
            )

    per_batch_df = pd.DataFrame(per_batch_rows)
    per_batch_df.sort_values(["Batch", "Policy"], inplace=True)

    overall_df = (
        per_batch_df.groupby("Policy", as_index=False)[
            ["Total CFP [gCO2e]", "Avg Wait [s]", "Makespan [s]"]
        ]
        .mean()
    )
    return TableResults(per_batch=per_batch_df, overall=overall_df)


def _format_latex(df: pd.DataFrame, float_fmt: str = "{:.3f}") -> str:
    formatted = df.copy()
    for column in ["Total CFP [gCO2e]", "Avg Wait [s]", "Makespan [s]"]:
        if column in formatted:
            formatted[column] = formatted[column].apply(
                lambda x: float_fmt.format(x) if pd.notna(x) else "nan"
            )
    if "Pareto Front" in formatted:
        formatted["Pareto Front"] = formatted["Pareto Front"].map({True: "True", False: "False"})
    return formatted.to_latex(index=False, escape=False)


def main() -> None:
    parser = argparse.ArgumentParser(description="Generate Kubernetes replay LaTeX tables.")
    parser.add_argument(
        "--results-root",
        type=Path,
        default=Path("analysis/k8s_results"),
        help="Root directory containing batch_<size>/summary.csv files.",
    )
    parser.add_argument(
        "--batches",
        type=int,
        nargs="+",
        default=list(DEFAULT_BATCHES),
        help="Batch sizes to include (default: %(default)s).",
    )
    parser.add_argument(
        "--out-dir",
        type=Path,
        default=Path("analysis/tables"),
        help="Destination directory for the LaTeX fragments.",
    )
    args = parser.parse_args()

    tables = build_k8s_tables(args.results_root, args.batches)

    args.out_dir.mkdir(parents=True, exist_ok=True)
    (args.out_dir / "k8s_per_batch.tex").write_text(_format_latex(tables.per_batch))
    (args.out_dir / "k8s_overall.tex").write_text(_format_latex(tables.overall))
    print(f"[generate_k8s_tables] wrote tables to {args.out_dir}")
    print("\nPer-batch:\n")
    print(_format_latex(tables.per_batch))
    print("\nOverall:\n")
    print(_format_latex(tables.overall))


if __name__ == "__main__":
    main()
