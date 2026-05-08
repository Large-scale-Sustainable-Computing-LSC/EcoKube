#!/usr/bin/env python3
from __future__ import annotations

import argparse
import warnings
from pathlib import Path

import matplotlib.pyplot as plt
import numpy as np
import pandas as pd
import seaborn as sns


def _save(fig, out: Path, stem: str) -> None:
    fig.tight_layout()
    fig.savefig(out / f"{stem}.png", dpi=300)
    fig.savefig(out / f"{stem}.pdf")
    plt.close(fig)


def _pick_policy_order(policies: list[str]) -> list[str]:
    preferred = ["ecokube", "keids", "topsis", "k8s", "carbonscaler"]
    seen = set(policies)
    ordered = [p for p in preferred if p in seen]
    ordered.extend([p for p in sorted(seen) if p not in ordered])
    return ordered


def _var_policy_order(policies: list[str]) -> list[str]:
    # For delta-vs-k8s plots (exclude k8s baseline itself)
    preferred = ["ecokube", "keids", "topsis", "carbonscaler"]
    seen = {p for p in policies if p != "k8s"}
    ordered = [p for p in preferred if p in seen]
    ordered.extend([p for p in sorted(seen) if p not in ordered])
    return ordered


def main() -> None:
    ap = argparse.ArgumentParser(description="Generate figures from partial summary runs")
    ap.add_argument("--summary", required=True, help="combined_partial_summary.csv path")
    ap.add_argument("--out-dir", required=True, help="output directory")
    ap.add_argument("--style", default=None, help="optional mplstyle path")
    args = ap.parse_args()

    if args.style:
        plt.style.use(args.style)

    out = Path(args.out_dir)
    out.mkdir(parents=True, exist_ok=True)

    df = pd.read_csv(args.summary)
    if df.empty:
        raise ValueError("Summary CSV is empty; cannot generate figures.")

    key_cols = ["policy", "ci_weight", "batch_size", "arrival_rate", "rep"]
    metrics = ["total_ci_cost_g", "makespan_s", "avg_wait_s"]
    d = df[key_cols + metrics].copy()

    if "k8s" not in d["policy"].unique():
        raise ValueError("k8s baseline not found in summary; delta-vs-k8s figures require k8s rows.")

    # Scenario-rep comparison against k8s
    base = d[d["policy"] == "k8s"].rename(
        columns={
            "total_ci_cost_g": "base_carbon",
            "makespan_s": "base_makespan",
            "avg_wait_s": "base_wait",
        }
    )
    m = d.merge(
        base[["ci_weight", "batch_size", "arrival_rate", "rep", "base_carbon", "base_makespan", "base_wait"]],
        on=["ci_weight", "batch_size", "arrival_rate", "rep"],
        how="inner",
    )
    if m.empty:
        raise ValueError("No overlap between policy rows and k8s baseline rows for scenario-rep comparison.")

    m["delta_carbon_pct"] = 100.0 * (m["total_ci_cost_g"] - m["base_carbon"]) / m["base_carbon"]
    m["delta_makespan_pct"] = 100.0 * (m["makespan_s"] - m["base_makespan"]) / m["base_makespan"]
    m["delta_wait_pct"] = 100.0 * (m["avg_wait_s"] - m["base_wait"]) / m["base_wait"]

    policies_all = _pick_policy_order(m["policy"].dropna().unique().tolist())
    policies_var = _var_policy_order(m["policy"].dropna().unique().tolist())

    # 1) Headline absolute-values bar chart (paper-style hatches)
    headline_order = [p for p in ["k8s", "keids", "topsis", "ecokube"] if p in policies_all]
    abs_mean = d.groupby("policy")[["total_ci_cost_g", "avg_wait_s", "makespan_s"]].mean().reindex(headline_order)
    abs_std = d.groupby("policy")[["total_ci_cost_g", "avg_wait_s", "makespan_s"]].std(ddof=1).reindex(headline_order).fillna(0.0)

    colors = {
        "k8s": "#e76f51",
        "keids": "#4c78d0",
        "topsis": "#7f7f7f",
        "ecokube": "#2ca25f",
    }
    hatches = {
        "k8s": "o",
        "keids": "x",
        "topsis": ".",
        "ecokube": "/",
    }
    metric_specs = [
        ("total_ci_cost_g", "Estimated emissions (g)"),
        ("avg_wait_s", "Avg wait (s)"),
        ("makespan_s", "Makespan (s)"),
    ]

    fig, axes = plt.subplots(1, 3, figsize=(12.0, 4.2), sharex=False)
    x = np.arange(len(headline_order))
    for ax, (metric_col, metric_label) in zip(axes, metric_specs):
        vals = abs_mean[metric_col].values
        errs = abs_std[metric_col].values
        bars = ax.bar(
            x,
            vals,
            yerr=errs,
            capsize=4,
            linewidth=1.0,
            edgecolor="black",
            color=[colors.get(p, "#999999") for p in headline_order],
        )
        for bar, pol in zip(bars, headline_order):
            bar.set_hatch(hatches.get(pol, ""))
        ax.set_xticks(x)
        ax.set_xticklabels(headline_order)
        ax.set_xlabel("Policy")
        ax.set_ylabel(metric_label)
        ax.set_title(metric_label)

    _save(fig, out, "policy_outcome_comparison")

    # 2) Pareto scatter: mean makespan vs mean carbon
    pareto = (
        d.groupby(["policy", "ci_weight", "batch_size", "arrival_rate"], as_index=False)[["total_ci_cost_g", "makespan_s"]]
        .mean()
    )
    fig = plt.figure(figsize=(7.2, 4.8))
    ax = sns.scatterplot(
        data=pareto,
        x="makespan_s",
        y="total_ci_cost_g",
        hue="policy",
        style="policy",
        hue_order=policies_all,
        alpha=0.8,
    )
    ax.set_xlabel("Makespan (s)")
    ax.set_ylabel("Total carbon proxy (g)")
    ax.set_title("Pareto view on completed scenarios")
    _save(fig, out, "partial_pareto_scatter")

    # Policy-only delta view (no k8s)
    box = m[m["policy"] != "k8s"].copy()
    if box.empty:
        raise ValueError("No non-k8s policies found; cannot generate delta distribution figures.")

    # 3) Robustness boxplot for carbon delta
    fig = plt.figure(figsize=(7.2, 4.4))
    ax = sns.boxplot(data=box, x="policy", y="delta_carbon_pct", order=policies_var)
    ax.axhline(0, color="black", linewidth=0.8)
    ax.set_xlabel("Policy")
    ax.set_ylabel("Carbon % change vs k8s")
    ax.set_title("Carbon robustness")
    _save(fig, out, "partial_carbon_robustness")

    # 4) Violin + box overlay for wait deltas
    fig = plt.figure(figsize=(8.0, 4.6))
    ax = sns.violinplot(data=box, x="policy", y="delta_wait_pct", order=policies_var, inner=None, cut=0)
    sns.boxplot(
        data=box,
        x="policy",
        y="delta_wait_pct",
        order=policies_var,
        width=0.22,
        showcaps=True,
        boxprops={"facecolor": "white", "zorder": 3},
        showfliers=False,
        whiskerprops={"linewidth": 1.1},
        ax=ax,
    )
    ax.axhline(0, color="black", linewidth=0.8)
    ax.set_xlabel("Policy")
    ax.set_ylabel("Wait % change vs k8s")
    ax.set_title("Latency trade-off distribution")
    _save(fig, out, "partial_wait_violin_box")

    # 5) ECDF for wait deltas
    fig = plt.figure(figsize=(8.0, 4.8))
    ax = sns.ecdfplot(data=box, x="delta_wait_pct", hue="policy", hue_order=policies_var, linewidth=2)
    ax.axvline(0, color="black", linewidth=0.8)
    ax.set_xlabel("Wait % change vs k8s")
    ax.set_ylabel("ECDF")
    ax.set_title("ECDF of latency deltas")
    _save(fig, out, "partial_wait_ecdf")

    # 6) Ridgeline-style KDE for carbon deltas (skip zero-variance safely)
    rid = box[["policy", "delta_carbon_pct"]].dropna().copy()
    policies = [p for p in policies_var if p in rid["policy"].unique()]
    fig, ax = plt.subplots(figsize=(8.4, 5.2))
    y_gap = 1.2
    warnings.filterwarnings("ignore", message="Dataset has 0 variance; skipping density estimate")
    for idx, pol in enumerate(policies):
        vals = rid.loc[rid["policy"] == pol, "delta_carbon_pct"].values
        if len(vals) < 3:
            continue
        if np.nanstd(vals) < 1e-12:
            continue
        prev_lines = len(ax.lines)
        prev_colls = len(ax.collections)
        sns.kdeplot(x=vals, fill=True, alpha=0.35, linewidth=1.2, ax=ax, label=pol, warn_singular=False)
        if len(ax.lines) == prev_lines:
            continue
        line = ax.lines[-1]
        x = line.get_xdata()
        if len(x) == 0:
            continue
        y = line.get_ydata() + idx * y_gap
        line.set_data(x, y)
        if len(ax.collections) > prev_colls:
            coll = ax.collections[-1]
            try:
                for pth in coll.get_paths():
                    pth.vertices[:, 1] += idx * y_gap
            except Exception:
                pass
        ax.text(np.min(x), idx * y_gap + 0.03, pol, fontsize=9)
    ax.axvline(0, color="black", linewidth=0.8)
    ax.set_yticks([])
    ax.set_xlabel("Carbon % change vs k8s")
    ax.set_title("Ridgeline-style carbon delta densities")
    _save(fig, out, "partial_carbon_ridgeline")

    # 7) Hexbin density carbon vs makespan deltas
    hex_df = box[["delta_makespan_pct", "delta_carbon_pct"]].dropna()
    fig, ax = plt.subplots(figsize=(7.2, 5.2))
    hb = ax.hexbin(hex_df["delta_makespan_pct"], hex_df["delta_carbon_pct"], gridsize=28, cmap="viridis", mincnt=1)
    cb = fig.colorbar(hb, ax=ax)
    cb.set_label("Scenario-rep density")
    ax.axhline(0, color="black", linewidth=0.8)
    ax.axvline(0, color="black", linewidth=0.8)
    ax.set_xlabel("Makespan % change vs k8s")
    ax.set_ylabel("Carbon % change vs k8s")
    ax.set_title("Carbon vs makespan trade-off density")
    _save(fig, out, "partial_tradeoff_hexbin")

    # 8) Paired scenario-wise dotplot (carbon deltas)
    scen = (
        box.groupby(["policy", "ci_weight", "batch_size", "arrival_rate", "rep"], as_index=False)["delta_carbon_pct"]
        .mean()
    )
    fig = plt.figure(figsize=(10.2, 4.6))
    ax = sns.stripplot(
        data=scen,
        x="policy",
        y="delta_carbon_pct",
        order=policies_var,
        jitter=0.2,
        alpha=0.45,
        size=3,
    )
    sns.pointplot(
        data=scen,
        x="policy",
        y="delta_carbon_pct",
        order=policies_var,
        estimator=np.mean,
        errorbar=("pi", 95),
        color="black",
        markers="d",
        linestyles="none",
        ax=ax,
    )
    ax.axhline(0, color="black", linewidth=0.8)
    ax.set_xlabel("Policy")
    ax.set_ylabel("Carbon % change vs k8s")
    ax.set_title("Scenario-wise paired deltas (dots) + mean/PI")
    _save(fig, out, "partial_paired_delta_dotplot")


if __name__ == "__main__":
    main()
