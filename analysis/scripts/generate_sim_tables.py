#!/usr/bin/env python3
"""
Generate LaTeX tables for the simulator sweeps.

The notebook can import this module and call :func:`build_sim_tables`
to obtain the per-batch and overall summaries as DataFrames, or it can
be executed as a script to emit pre-formatted LaTeX tables.
"""

from __future__ import annotations

import argparse
import json
from dataclasses import dataclass
from pathlib import Path
from typing import Dict, Iterable, List, Tuple

import numpy as np
import pandas as pd

POLICY_LABELS = {
    "hetpolicy": "HetPolicy",
    "hetpolicy_het_weighted_sum_a0p85_b0p10_g0p05": "HetPolicy",
    "topsis": "TOPSIS",
    "keids": "KEIDS",
    "k8s": "Kubernetes base",
    "k8": "Kubernetes base",
}

POLICY_PRIORITY = {
    "hetpolicy": 0,
    "hetpolicy_het_weighted_sum_a0p85_b0p10_g0p05": 0,
    "carbonscaler": 1,
    "topsis": 2,
    "keids": 3,
    "k8": 4,
    "k8s": 4,
}

DEFAULT_BATCHES = (200, 500, 1000)
DEFAULT_CI_WEIGHT = 0.20


@dataclass
class TableResults:
    per_batch: pd.DataFrame
    overall: pd.DataFrame


def _latest_results_dir(root: Path) -> Path:
    candidates = sorted(root.glob("results_*"))
    if not candidates:
        raise FileNotFoundError(f"No results_* directories under {root}")
    return candidates[-1]


def _read_summary(path: Path, policies: Iterable[str]) -> pd.DataFrame:
    if not path.exists():
        raise FileNotFoundError(path)
    df = pd.read_csv(path)
    df = df[df["policy"].str.lower().isin(policies)].copy()
    df["policy"] = df["policy"].str.lower()
    return df


def _compute_pareto(df: pd.DataFrame) -> pd.Series:
    """Return boolean Series marking Pareto-efficient points for (total_ci_cost_g, makespan_s)."""
    values = df[["total_ci_cost_g", "makespan_s"]].to_numpy()
    dominated = []
    for i, point in enumerate(values):
        mask = (values[:, 0] <= point[0]) & (values[:, 1] <= point[1])
        mask[i] = False  # ignore self
        strictly_better = (values[mask][:, 0] < point[0]) | (values[mask][:, 1] < point[1])
        dominated.append(strictly_better.any())
    return pd.Series(~pd.Series(dominated).to_numpy(), index=df["policy"])


def _build_from_combined(
    combined_path: Path,
    ci_weight: float,
    batches: Iterable[int],
    policies: Iterable[str],
) -> TableResults:
    df = pd.read_csv(combined_path)
    df["policy"] = df["policy"].str.lower()
    df = df[df["policy"].isin(policies)].copy()
    df = df[df["batch_size"].isin(batches)]
    df = df[np.isclose(df["ci_weight"], ci_weight)]
    if df.empty:
        raise ValueError(
            f"No entries found in {combined_path} for ci_weight={ci_weight} and batches={list(batches)}"
        )

    grouped = (
        df.groupby(["batch_size", "policy"], as_index=False)[
            ["total_ci_cost_g", "avg_wait_s", "makespan_s"]
        ]
        .mean()
    )

    per_batch_rows: List[Dict[str, object]] = []
    for batch in batches:
        subset = grouped[grouped["batch_size"] == batch].copy()
        if subset.empty:
            continue
        subset["policy_order"] = subset["policy"].map(POLICY_PRIORITY).fillna(999)
        ordered = subset.sort_values(
            ["total_ci_cost_g", "avg_wait_s", "makespan_s", "policy_order"]
        ).drop_duplicates(subset=["total_ci_cost_g", "avg_wait_s", "makespan_s"], keep="first")
        pareto = _compute_pareto(ordered)
        pareto_map = {row["policy"]: bool(pareto.loc[row["policy"]]) for _, row in ordered.iterrows()}
        for _, row in subset.iterrows():
            label = POLICY_LABELS.get(row["policy"], row["policy"])
            per_batch_rows.append(
                {
                    "Policy": label,
                    "Batch": batch,
                    "CI Weight": ci_weight,
                    "Total CFP [gCO2e]": row["total_ci_cost_g"],
                    "Avg Wait [s]": row["avg_wait_s"],
                    "Makespan [s]": row["makespan_s"],
                    "Pareto Front": pareto_map.get(row["policy"], False),
                }
            )

    per_batch_df = pd.DataFrame(per_batch_rows)
    per_batch_df.sort_values(["Batch", "Policy"], inplace=True)

    pareto_flags: List[bool] = []
    for batch in per_batch_df["Batch"].unique():
        mask = per_batch_df["Batch"] == batch
        subset = per_batch_df.loc[mask, ["Total CFP [gCO2e]", "Avg Wait [s]"]].to_numpy()
        for i, point in enumerate(subset):
            other = np.delete(subset, i, axis=0)
            dominated = any(
                (row[0] <= point[0] and row[1] <= point[1])
                and (row[0] < point[0] or row[1] < point[1])
                for row in other
            )
            pareto_flags.append(not dominated)
    per_batch_df["Pareto Front"] = pareto_flags

    overall_df = (
        per_batch_df.groupby("Policy", as_index=False)[
            ["Total CFP [gCO2e]", "Avg Wait [s]", "Makespan [s]"]
        ].mean()
    )
    return TableResults(per_batch=per_batch_df, overall=overall_df)


def build_sim_tables(
    results_root: Path,
    ci_weight: float = DEFAULT_CI_WEIGHT,
    batches: Iterable[int] = DEFAULT_BATCHES,
    policies: Iterable[str] = ("hetpolicy", "hetpolicy_het_weighted_sum_a0p85_b0p10_g0p05", "topsis", "keids", "k8s"),
    combined_summary: Path | None = None,
) -> TableResults:
    """Read simulator summary CSVs and return per-batch plus overall tables."""

    if combined_summary is not None and combined_summary.exists():
        try:
            return _build_from_combined(combined_summary, ci_weight, batches, list(policies))
        except ValueError:
            pass

    per_batch_rows: List[Dict[str, object]] = []
    policies = list(policies)

    for batch in batches:
        summary_path = results_root / f"ci_{str(ci_weight).replace('.', 'p')}_bs_{batch}/summary.csv"
        df = _read_summary(summary_path, policies)
        df.sort_values("policy", inplace=True)
        df["policy_order"] = df["policy"].map(POLICY_PRIORITY).fillna(999)
        ordered = df.sort_values(
            ["total_ci_cost_g", "avg_wait_s", "makespan_s", "policy_order"]
        ).drop_duplicates(subset=["total_ci_cost_g", "avg_wait_s", "makespan_s"], keep="first")
        pareto = _compute_pareto(ordered)
        pareto_map = {row["policy"]: bool(pareto.loc[row["policy"]]) for _, row in ordered.iterrows()}
        for _, row in df.iterrows():
            label = POLICY_LABELS.get(row["policy"], row["policy"])
            per_batch_rows.append(
                {
                    "Policy": label,
                    "Batch": batch,
                    "CI Weight": ci_weight,
                    "Total CFP [gCO2e]": row["total_ci_cost_g"],
                    "Avg Wait [s]": row["avg_wait_s"],
                    "Makespan [s]": row["makespan_s"],
                    "Pareto Front": pareto_map.get(row["policy"], False),
                }
            )

    per_batch_df = pd.DataFrame(per_batch_rows)
    per_batch_df.sort_values(["Batch", "Policy"], inplace=True)

    pareto_flags: List[bool] = []
    for batch in per_batch_df["Batch"].unique():
        mask = per_batch_df["Batch"] == batch
        subset = per_batch_df.loc[mask, ["Total CFP [gCO2e]", "Avg Wait [s]"]].to_numpy()
        for i, point in enumerate(subset):
            other = np.delete(subset, i, axis=0)
            dominated = any(
                (row[0] <= point[0] and row[1] <= point[1])
                and (row[0] < point[0] or row[1] < point[1])
                for row in other
            )
            pareto_flags.append(not dominated)
    per_batch_df["Pareto Front"] = pareto_flags

    overall_df = (
        per_batch_df.groupby("Policy", as_index=False)[
            ["Total CFP [gCO2e]", "Avg Wait [s]", "Makespan [s]"]
        ]
        .mean()
    )

    return TableResults(per_batch=per_batch_df, overall=overall_df)


def _to_latex(df: pd.DataFrame, float_fmt: str = "{:.3f}") -> str:
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
    parser = argparse.ArgumentParser(description="Generate simulator LaTeX tables.")
    parser.add_argument(
        "--results-root",
        type=Path,
        default=None,
        help="Path to the simulator results directory (defaults to newest in kubenergysched/results).",
    )
    parser.add_argument(
        "--ci-weight",
        type=float,
        default=DEFAULT_CI_WEIGHT,
        help="CI weight to select (default: %(default)s).",
    )
    parser.add_argument(
        "--batches",
        type=int,
        nargs="+",
        default=list(DEFAULT_BATCHES),
        help="Batch sizes to include (default: %(default)s).",
    )
    parser.add_argument(
        "--combined-summary",
        type=Path,
        default=Path("analysis/results/combined_summary.csv"),
        help="Path to combined_summary.csv (if present, preferred over per-run summaries).",
    )
    parser.add_argument("--out-dir", type=Path, default=Path("analysis/tables"), help="Directory for LaTeX files.")
    args = parser.parse_args()

    repo_root = Path(__file__).resolve().parents[2]
    default_root = repo_root / "kubenergysched" / "results"
    results_root = args.results_root
    if results_root is None:
        results_root = _latest_results_dir(default_root)
    results_root = results_root.resolve()

    tables = build_sim_tables(
        results_root,
        ci_weight=args.ci_weight,
        batches=args.batches,
        combined_summary=args.combined_summary,
    )

    args.out_dir.mkdir(parents=True, exist_ok=True)
    (args.out_dir / "sim_per_batch.tex").write_text(_to_latex(tables.per_batch))
    (args.out_dir / "sim_overall.tex").write_text(_to_latex(tables.overall))

    print(f"[generate_sim_tables] wrote tables to {args.out_dir}")
    print("\nPer-batch:\n")
    print(_to_latex(tables.per_batch))
    print("\nOverall:\n")
    print(_to_latex(tables.overall))


if __name__ == "__main__":
    main()
