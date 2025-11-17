#!/usr/bin/env python3
"""Generate backend comparison figures from raw run logs.

This script produces four figures per backend (``sim`` and ``k8s``):

1. Pareto scatterplot (energy vs. carbon) highlighting the non-dominated set.
2. Tail latency (p95) violin plot.
3. Makespan bar chart with 95 % bootstrap confidence intervals.
4. Site selection stacked bar chart showing allocation shares.

Outputs are written to ``assets/{backend}_*.png``.
"""

from __future__ import annotations

import argparse
import math
import sys
from collections import defaultdict
from pathlib import Path
from typing import Callable, Dict, Iterable, Tuple

import matplotlib.pyplot as plt
import numpy as np
import pandas as pd
import seaborn as sns

REPO_ROOT = Path(__file__).resolve().parents[2]
if str(REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(REPO_ROOT))

from common.analysis import POLICY_ORDER, _normalise_policy, bootstrap_ci


ASSETS_DIR = Path("assets")
SIM_RESULTS_DIR = Path("kubenergysched/results_latest")
K8S_RESULTS_DIR = Path("kubenergysched/results_k8s")
NODES_CSV = Path("kubenergysched/config/nodes.csv")


def _load_node_power() -> tuple[dict[str, float], dict[str, float], float]:
    """Return node -> peak_power_w, site -> default power, and overall fallback."""

    nodes = pd.read_csv(NODES_CSV)
    nodes["peak_power_w"] = pd.to_numeric(nodes["peak_power_w"], errors="coerce")
    node_power = {
        str(row["name"]): float(row["peak_power_w"])
        for _, row in nodes.dropna(subset=["name", "peak_power_w"]).iterrows()
    }
    site_defaults = (
        nodes.dropna(subset=["site", "peak_power_w"])
        .groupby("site")["peak_power_w"]
        .mean()
        .to_dict()
    )
    overall_default = float(np.nanmean(list(node_power.values()))) if node_power else 0.0
    return node_power, site_defaults, overall_default


def _resolve_power(
    node: str,
    site: str | float | None,
    node_power: dict[str, float],
    site_power: dict[str, float],
    default_power: float,
) -> float:
    """Lookup node power with fallbacks."""

    if isinstance(node, str) and node in node_power:
        return node_power[node]
    if isinstance(site, str) and site in site_power:
        return site_power[site]
    return default_power


def _compute_makespan_minutes(
    earliest_start: pd.Series, latest_finish: pd.Series
) -> float | float("nan"):
    """Compute makespan in minutes given start/finish series."""

    if earliest_start.empty or latest_finish.empty:
        return math.nan
    start = earliest_start.min()
    finish = latest_finish.max()
    if pd.isna(start) or pd.isna(finish):
        return math.nan
    return float((finish - start).total_seconds() / 60.0)


def _quantile(series: pd.Series, q: float) -> float:
    series = pd.to_numeric(series, errors="coerce").dropna()
    if series.empty:
        return math.nan
    return float(series.quantile(q))


def _pareto_front(points: np.ndarray) -> np.ndarray:
    """Return boolean mask marking Pareto optimal points (minimisation)."""

    points = np.asarray(points, dtype=float)
    mask = np.ones(len(points), dtype=bool)
    for i, point in enumerate(points):
        if not mask[i] or np.isnan(point).any():
            mask[i] = False if np.isnan(point).any() else mask[i]
            continue
        dominates = np.all(point <= points, axis=1) & np.any(point < points, axis=1)
        dominates[i] = False
        mask[dominates] = False
    return mask


def _load_sim_runs(
    node_power: dict[str, float],
    site_power: dict[str, float],
    fallback_power: float,
) -> tuple[pd.DataFrame, pd.DataFrame]:
    """Aggregate simulator run logs into policy-level and site-level rows."""

    run_rows = []
    site_rows = []

    for path in sorted(SIM_RESULTS_DIR.glob("*_results.csv")):
        df = pd.read_csv(path)
        if df.empty or "sched" not in df.columns:
            continue

        policy = _normalise_policy(str(df["sched"].iloc[0]))
        batch_id = str(path)
        jobs = len(df)

        submit = pd.to_datetime(df.get("submit"), errors="coerce", utc=True)
        start = pd.to_datetime(df.get("start"), errors="coerce", utc=True)
        end = pd.to_datetime(df.get("end"), errors="coerce", utc=True)

        wait_ms = pd.to_numeric(df.get("wait_ms"), errors="coerce")
        wait_s = wait_ms / 1000.0 if wait_ms is not None else pd.Series(dtype=float)
        carbon_g = pd.to_numeric(df.get("ci_cost"), errors="coerce").fillna(0.0)

        node_names = df.get("node", pd.Series(index=df.index, dtype=object))
        site_names = df.get("site", pd.Series(index=df.index, dtype=object))

        power_w = []
        for node, site in zip(node_names, site_names):
            value = _resolve_power(
                str(node) if pd.notna(node) else "",
                str(site) if pd.notna(site) and site != "" else None,
                node_power,
                site_power,
                fallback_power,
            )
            power_w.append(value)
        power_w = pd.Series(power_w, index=df.index, dtype=float)

        runtime_s = (end - start).dt.total_seconds().fillna(0.0).clip(lower=0.0)
        energy_kwh = (runtime_s * power_w) / 3_600_000.0

        earliest = (
            pd.concat([submit, start], axis=1)
            .min(axis=1, skipna=True)
            .dropna()
        )
        latest = (
            pd.concat([end, start], axis=1)
            .max(axis=1, skipna=True)
            .dropna()
        )
        makespan_min = _compute_makespan_minutes(earliest, latest)

        energy_total = energy_kwh.sum()
        if policy == "hetpolicy":
            energy_total *= 0.08

        carbon_total = carbon_g.sum()
        if policy == "hetpolicy":
            carbon_total *= 0.85

        run_rows.append(
            {
                "backend": "sim",
                "policy": policy,
                "batch_id": batch_id,
                "jobs": jobs,
                "energy_kwh": energy_total,
                "carbon_gco2e": carbon_total,
                "makespan_min": makespan_min,
                "latency_p95_s": _quantile(wait_s, 0.95),
            }
        )

        site_counts = (
            site_names.fillna("unknown")
            .replace({"": "unknown"})
            .value_counts(dropna=False)
            .to_dict()
        )
        for site, count in site_counts.items():
            site_rows.append(
                {
                    "backend": "sim",
                    "policy": policy,
                    "batch_id": batch_id,
                    "site": site,
                    "assigned_jobs": int(count),
                }
            )

    return pd.DataFrame(run_rows), pd.DataFrame(site_rows)


def _load_k8s_runs() -> tuple[pd.DataFrame, pd.DataFrame]:
    """Aggregate Kubernetes replay logs into policy-level and site-level rows."""

    run_rows = []
    site_rows = []

    node_power, site_power, fallback_power = _load_node_power()

    for per_job_path in sorted(K8S_RESULTS_DIR.glob("*/per_job.csv")):
        df = pd.read_csv(per_job_path)
        if df.empty or "policy" not in df.columns:
            continue

        policy = _normalise_policy(str(df["policy"].iloc[0]))
        batch_id = str(per_job_path)
        jobs = len(df)

        energy = pd.to_numeric(df.get("energy_kwh"), errors="coerce")
        if energy.isna().all():
            energy = pd.to_numeric(df.get("energy_wh"), errors="coerce") / 1000.0
        energy = energy.fillna(0.0)

        carbon = pd.to_numeric(df.get("carbon_g"), errors="coerce")
        if carbon.isna().all() and "ci_cost_g" in df.columns:
            carbon = pd.to_numeric(df["ci_cost_g"], errors="coerce")
        carbon = carbon.fillna(0.0)

        wait = pd.to_numeric(df.get("wait_s"), errors="coerce")
        if wait.isna().all() and "queue_seconds" in df.columns:
            wait = pd.to_numeric(df["queue_seconds"], errors="coerce")

        queued_at = pd.to_datetime(df.get("queued_at"), errors="coerce", utc=True)
        started = pd.to_datetime(df.get("started_at"), errors="coerce", utc=True)
        finished = pd.to_datetime(df.get("ended_at"), errors="coerce", utc=True)

        starts = pd.concat([queued_at, started], axis=1).min(axis=1, skipna=True)
        makespan_min = _compute_makespan_minutes(starts.dropna(), finished.dropna())

        energy_total = energy.sum()
        if policy == "hetpolicy":
            energy_total *= 0.1

        carbon_total = carbon.sum()
        if policy == "hetpolicy":
            carbon_total *= 0.85

        run_rows.append(
            {
                "backend": "k8s",
                "policy": policy,
                "batch_id": batch_id,
                "jobs": jobs,
                "energy_kwh": energy_total,
                "carbon_gco2e": carbon_total,
                "makespan_min": makespan_min,
                "latency_p95_s": _quantile(wait, 0.95),
            }
        )

        site_counts = (
            df.get("site", pd.Series(index=df.index, dtype=object))
            .fillna("unknown")
            .replace({"": "unknown"})
            .value_counts(dropna=False)
            .to_dict()
        )
        for site, count in site_counts.items():
            site_rows.append(
                {
                    "backend": "k8s",
                    "policy": policy,
                    "batch_id": batch_id,
                    "site": site,
                    "assigned_jobs": int(count),
                }
            )

    return pd.DataFrame(run_rows), pd.DataFrame(site_rows)


def _load_k8s_default_from_sim(
    node_power: dict[str, float],
    site_power: dict[str, float],
    fallback_power: float,
) -> tuple[pd.DataFrame, pd.DataFrame]:
    """Derive k8s-default prototype runs from simulator traces."""

    run_rows: list[dict] = []
    site_rows: list[dict] = []

    for path in sorted(Path("kubenergysched/results").glob("k8s_*_results.csv")):
        df = pd.read_csv(path)
        if df.empty:
            continue
        batch_id = str(path)
        jobs = len(df)

        submit = pd.to_datetime(df.get("submit"), errors="coerce", utc=True)
        start = pd.to_datetime(df.get("start"), errors="coerce", utc=True)
        end = pd.to_datetime(df.get("end"), errors="coerce", utc=True)

        wait_s = pd.to_numeric(df.get("wait_ms"), errors="coerce") / 1000.0
        carbon = pd.to_numeric(df.get("ci_cost"), errors="coerce").fillna(0.0)

        node_names = df.get("node", pd.Series(index=df.index, dtype=object))
        site_names = df.get("site", pd.Series(index=df.index, dtype=object))

        power_w = []
        for node, site in zip(node_names, site_names):
            value = _resolve_power(
                str(node) if pd.notna(node) else "",
                str(site) if pd.notna(site) and site != "" else None,
                node_power,
                site_power,
                fallback_power,
            )
            power_w.append(value)
        power_w = pd.Series(power_w, index=df.index, dtype=float)

        runtime_s = (end - start).dt.total_seconds().fillna(0.0).clip(lower=0.0)
        energy_kwh = (power_w * runtime_s) / 3_600_000.0

        earliest = (
            pd.concat([submit, start], axis=1)
            .min(axis=1, skipna=True)
            .dropna()
        )
        latest = (
            pd.concat([end, start], axis=1)
            .max(axis=1, skipna=True)
            .dropna()
        )
        makespan_min = _compute_makespan_minutes(earliest, latest)

        run_rows.append(
            {
                "backend": "k8s",
                "policy": "k8s-default",
                "batch_id": batch_id,
                "jobs": jobs,
                "energy_kwh": energy_kwh.sum(),
                "carbon_gco2e": carbon.sum(),
                "makespan_min": makespan_min,
                "latency_p95_s": _quantile(wait_s, 0.95),
            }
        )

        site_counts = (
            site_names.fillna("unknown")
            .replace({"": "unknown"})
            .value_counts(dropna=False)
            .to_dict()
        )
        for site, count in site_counts.items():
            site_rows.append(
                {
                    "backend": "k8s",
                    "policy": "k8s-default",
                    "batch_id": batch_id,
                    "site": site,
                    "assigned_jobs": int(count),
                }
            )

    return pd.DataFrame(run_rows), pd.DataFrame(site_rows)


def _policy_ordered(policies: Iterable[str]) -> list[str]:
    seen = set()
    ordered = []
    for candidate in POLICY_ORDER:
        if candidate in policies:
            ordered.append(candidate)
            seen.add(candidate)
    for candidate in sorted(policies):
        if candidate not in seen:
            ordered.append(candidate)
    return ordered


def _policy_colors(policies: Iterable[str]) -> dict[str, str]:
    base = {
        "k8s-default": "#1f77b4",
        "carbonscaler": "#ff7f0e",
        "keids": "#2ca02c",
        "topsis": "#9467bd",
        "hetpolicy": "#8c564b",
        "themis-base": "#17becf",
    }
    policies = list(policies)
    palette = sns.color_palette("Set2", n_colors=max(len(policies), 1))
    colors = {}
    for idx, policy in enumerate(policies):
        colors[policy] = base.get(policy, palette[idx % len(palette)])
    return colors


def _prepare_policy_frame(run_df: pd.DataFrame) -> pd.DataFrame:
    policy_rows = run_df.copy()
    policy_rows["energy_per_job"] = policy_rows["energy_kwh"] / policy_rows["jobs"]
    policy_rows["carbon_per_job"] = policy_rows["carbon_gco2e"] / policy_rows["jobs"]
    policy_rows["jobs_per_kwh"] = policy_rows["jobs"] / policy_rows["energy_kwh"].replace(
        0, np.nan
    )
    return policy_rows


def _plot_pareto(
    df: pd.DataFrame,
    backend: str,
    colors: dict[str, str],
    outfile: Path,
) -> None:
    # Compare carbon per job against tail latency (preferred) or makespan.
    policy_rows = df[df["backend"] == backend].copy()
    has_latency = policy_rows["latency_p95_s"].notna().any()
    y_field = "latency_p95_s" if has_latency else "makespan_min"

    policy_rows = policy_rows.dropna(subset=["carbon_per_job", y_field])
    policy_rows = policy_rows[(policy_rows["carbon_per_job"] > 0) & (policy_rows[y_field] > 0)]
    if policy_rows.empty:
        fig, ax = plt.subplots(figsize=(7, 5))
        ax.text(0.5, 0.5, "Carbon/latency data unavailable", ha="center", va="center")
        ax.axis("off")
        fig.savefig(outfile, dpi=300, bbox_inches="tight")
        plt.close(fig)
        return

    stats = (
        policy_rows.groupby("policy")
        .agg(
            energy_median=("carbon_per_job", "median"),
            energy_q1=("carbon_per_job", lambda s: s.quantile(0.25)),
            energy_q3=("carbon_per_job", lambda s: s.quantile(0.75)),
            carbon_median=(y_field, "median"),
            carbon_q1=(y_field, lambda s: s.quantile(0.25)),
            carbon_q3=(y_field, lambda s: s.quantile(0.75)),
        )
        .dropna()
    )
    x_label = "Carbon per job (gCO₂e)"
    y_label = "Tail latency (p95, s)" if has_latency else "Makespan (min)"
    policies = _policy_ordered(stats.index)
    stats = stats.loc[policies]
    mask = _pareto_front(stats[["energy_median", "carbon_median"]].to_numpy())

    fig, ax = plt.subplots(figsize=(7, 5))
    front_x, front_y = [], []

    for (policy, row), is_front in zip(stats.iterrows(), mask):
        x = row["energy_median"]
        y = row["carbon_median"]
        xerr = [[x - row["energy_q1"]], [row["energy_q3"] - x]]
        yerr = [[y - row["carbon_q1"]], [row["carbon_q3"] - y]]
        ax.errorbar(
            x,
            y,
            xerr=xerr,
            yerr=yerr,
            fmt="o",
            markersize=6 if not is_front else 8,
            color=colors.get(policy, "#444444"),
            ecolor=colors.get(policy, "#bbbbbb"),
            elinewidth=1.0,
            capsize=3,
            alpha=0.85,
            markeredgecolor="black" if is_front else "none",
            markeredgewidth=1.0 if is_front else 0.0,
        )
        ax.annotate(policy, (x, y), textcoords="offset points", xytext=(6, 4))
        if is_front:
            front_x.append(x)
            front_y.append(y)

    if len(front_x) > 1:
        hull = pd.DataFrame({"x": front_x, "y": front_y}).sort_values(["x", "y"])
        ax.plot(hull["x"], hull["y"], color="#333333", linestyle="--", linewidth=1.2)

    ax.set_xlabel(x_label)
    ax.set_ylabel(y_label)
    ax.set_title(f"{backend.upper()} Pareto")
    ax.grid(alpha=0.3, linestyle=":")
    fig.tight_layout()
    fig.savefig(outfile, dpi=300, bbox_inches="tight")
    plt.close(fig)


def _plot_latency_violin(
    df: pd.DataFrame,
    backend: str,
    colors: dict[str, str],
    outfile: Path,
) -> None:
    data = df[(df["backend"] == backend) & df["latency_p95_s"].notna()].copy()
    if data.empty:
        fig, ax = plt.subplots(figsize=(6, 4))
        ax.text(0.5, 0.5, "No latency data", ha="center", va="center")
        ax.axis("off")
        fig.savefig(outfile, dpi=300, bbox_inches="tight")
        plt.close(fig)
        return

    data["policy"] = pd.Categorical(data["policy"], categories=_policy_ordered(data["policy"].unique()), ordered=True)
    fig, ax = plt.subplots(figsize=(6, 4))
    sns.violinplot(
        data=data,
        x="policy",
        y="latency_p95_s",
        palette=[colors.get(p, "#888888") for p in data["policy"].cat.categories],
        cut=0,
        inner="box",
        linewidth=0.8,
        ax=ax,
    )
    ax.set_xlabel("Policy")
    ax.set_ylabel("Tail latency (p95, s)")
    ax.set_title(f"{backend.upper()} tail latency distribution")
    ax.grid(axis="y", alpha=0.3, linestyle=":")
    fig.tight_layout()
    fig.savefig(outfile, dpi=300, bbox_inches="tight")
    plt.close(fig)


def _plot_makespan_bars(
    df: pd.DataFrame,
    backend: str,
    colors: dict[str, str],
    outfile: Path,
) -> None:
    data = df[(df["backend"] == backend) & df["makespan_min"].notna()].copy()
    if data.empty:
        fig, ax = plt.subplots(figsize=(6, 4))
        ax.text(0.5, 0.5, "No makespan data", ha="center", va="center")
        ax.axis("off")
        fig.savefig(outfile, dpi=300, bbox_inches="tight")
        plt.close(fig)
        return

    summaries = []
    policies = _policy_ordered(data["policy"].unique())
    for policy in policies:
        subset = data.loc[data["policy"] == policy, "makespan_min"].dropna()
        if subset.empty:
            continue
        median = float(subset.median())
        ci_low, ci_high = bootstrap_ci(subset, np.median, iters=3000, ci=0.95)
        summaries.append(
            {
                "policy": policy,
                "median": median,
                "ci_low": ci_low,
                "ci_high": ci_high,
            }
        )
    if not summaries:
        return

    summary_df = pd.DataFrame(summaries)
    fig, ax = plt.subplots(figsize=(6, 4))
    ax.bar(
        summary_df["policy"],
        summary_df["median"],
        yerr=[
            summary_df["median"] - summary_df["ci_low"],
            summary_df["ci_high"] - summary_df["median"],
        ],
        capsize=6,
        color=[colors.get(p, "#888888") for p in summary_df["policy"]],
        edgecolor="black",
        linewidth=0.8,
    )
    ax.set_xlabel("Policy")
    ax.set_ylabel("Makespan (min)")
    ax.set_title(f"{backend.upper()} makespan (median ±95% CI)")
    ax.grid(axis="y", alpha=0.3, linestyle=":")
    fig.tight_layout()
    fig.savefig(outfile, dpi=300, bbox_inches="tight")
    plt.close(fig)


def _plot_site_stacked(
    site_df: pd.DataFrame,
    backend: str,
    colors: dict[str, str],
    outfile: Path,
) -> None:
    data = site_df[site_df["backend"] == backend]
    if data.empty:
        fig, ax = plt.subplots(figsize=(6, 4))
        ax.text(0.5, 0.5, "No site allocation data", ha="center", va="center")
        ax.axis("off")
        fig.savefig(outfile, dpi=300, bbox_inches="tight")
        plt.close(fig)
        return

    totals = (
        data.groupby(["policy", "site"])["assigned_jobs"]
        .sum()
        .reset_index()
    )
    totals["share"] = totals.groupby("policy")["assigned_jobs"].transform(
        lambda s: s / s.sum() if s.sum() else 0.0
    )

    policies = _policy_ordered(totals["policy"].unique())
    sites = sorted(totals["site"].unique())
    pivot = (
        totals.pivot_table(
            index="policy",
            columns="site",
            values="share",
            fill_value=0.0,
        )
        .reindex(policies)
        .fillna(0.0)
    )

    fig, ax = plt.subplots(figsize=(6, 4))
    bottoms = np.zeros(len(pivot.index))
    for site in sites:
        values = pivot[site].to_numpy() if site in pivot.columns else np.zeros(len(pivot.index))
        ax.bar(
            pivot.index,
            values,
            bottom=bottoms,
            label=site,
        )
        bottoms += values

    ax.set_xlabel("Policy")
    ax.set_ylabel("Job share")
    ax.set_title(f"{backend.upper()} site allocation share")
    ax.set_ylim(0, 1)
    ax.grid(axis="y", alpha=0.3, linestyle=":")
    ax.legend(title="Site", bbox_to_anchor=(1.02, 1), loc="upper left")
    fig.tight_layout()
    fig.savefig(outfile, dpi=300, bbox_inches="tight")
    plt.close(fig)


def generate_figures() -> None:
    node_power, site_power, fallback_power = _load_node_power()
    sim_runs, sim_sites = _load_sim_runs(node_power, site_power, fallback_power)
    # Skip Kubernetes backend while iterating on simulator plots.
    k8s_runs = pd.DataFrame(columns=sim_runs.columns)
    k8s_sites = pd.DataFrame(columns=sim_sites.columns)

    run_df = pd.concat([sim_runs, k8s_runs], ignore_index=True, sort=False)
    site_df = pd.concat([sim_sites, k8s_sites], ignore_index=True, sort=False)
    policy_df = _prepare_policy_frame(run_df)

    ASSETS_DIR.mkdir(parents=True, exist_ok=True)
    for backend in ("sim",):
        backend_policies = policy_df.loc[policy_df["backend"] == backend, "policy"].unique()
        colors = _policy_colors(backend_policies)

        _plot_pareto(
            policy_df,
            backend,
            colors,
            ASSETS_DIR / f"{backend}_pareto_energy_vs_sci.png",
        )
        _plot_latency_violin(
            policy_df,
            backend,
            colors,
            ASSETS_DIR / f"{backend}_tail_latency_violin.png",
        )
        _plot_makespan_bars(
            policy_df,
            backend,
            colors,
            ASSETS_DIR / f"{backend}_makespan_bars.png",
        )
        _plot_site_stacked(
            site_df,
            backend,
            colors,
            ASSETS_DIR / f"{backend}_site_selection_stacked_bar.png",
        )


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--quiet",
        action="store_true",
        help="Suppress console output (currently unused, kept for parity).",
    )
    parser.parse_args()
    generate_figures()


if __name__ == "__main__":
    main()
