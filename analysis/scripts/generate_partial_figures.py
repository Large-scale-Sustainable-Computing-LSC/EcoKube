#!/usr/bin/env python3
from __future__ import annotations

import argparse
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

    key_cols = ["policy", "ci_weight", "batch_size", "arrival_rate", "rep"]
    metrics = ["total_ci_cost_g", "makespan_s", "avg_wait_s"]
    d = df[key_cols + metrics].copy()

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
    m["delta_carbon_pct"] = 100.0 * (m["total_ci_cost_g"] - m["base_carbon"]) / m["base_carbon"]
    m["delta_makespan_pct"] = 100.0 * (m["makespan_s"] - m["base_makespan"]) / m["base_makespan"]
    m["delta_wait_pct"] = 100.0 * (m["avg_wait_s"] - m["base_wait"]) / m["base_wait"]

    order = ["ecokube", "carbonscaler", "keids", "topsis", "k8s"]

    # 1) Headline deltas bar chart
    headline = (
        m.groupby("policy")[["delta_carbon_pct", "delta_makespan_pct", "delta_wait_pct"]]
        .mean()
        .reset_index()
    )
    long = headline.melt(id_vars="policy", var_name="metric", value_name="delta_pct")
    metric_names = {
        "delta_carbon_pct": "Carbon footprint",
        "delta_makespan_pct": "Makespan",
        "delta_wait_pct": "Avg wait",
    }
    long["metric"] = long["metric"].map(metric_names)

    fig = plt.figure(figsize=(8, 4.2))
    ax = sns.barplot(data=long, x="policy", y="delta_pct", hue="metric", order=order)
    ax.axhline(0, color="black", linewidth=0.8)
    ax.set_xlabel("Policy")
    ax.set_ylabel("Mean % change vs k8s")
    ax.set_title("Partial run headline deltas")
    _save(fig, out, "partial_headline_deltas")

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
        hue_order=order,
        alpha=0.8,
    )
    ax.set_xlabel("Makespan (s)")
    ax.set_ylabel("Total carbon proxy (g)")
    ax.set_title("Pareto view on completed scenarios")
    _save(fig, out, "partial_pareto_scatter")

    # 3) Robustness boxplot for carbon delta
    box = m[m["policy"] != "k8s"].copy()
    fig = plt.figure(figsize=(7.2, 4.4))
    ax = sns.boxplot(data=box, x="policy", y="delta_carbon_pct", order=["ecokube", "carbonscaler", "keids", "topsis"])
    ax.axhline(0, color="black", linewidth=0.8)
    ax.set_xlabel("Policy")
    ax.set_ylabel("Carbon % change vs k8s")
    ax.set_title("Carbon robustness")
    _save(fig, out, "partial_carbon_robustness")

    # 4) Violin + box overlay for wait deltas
    fig = plt.figure(figsize=(8.0, 4.6))
    ax = sns.violinplot(data=box, x="policy", y="delta_wait_pct", order=["ecokube", "carbonscaler", "keids", "topsis"], inner=None, cut=0)
    sns.boxplot(
        data=box,
        x="policy",
        y="delta_wait_pct",
        order=["ecokube", "carbonscaler", "keids", "topsis"],
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
    ax = sns.ecdfplot(data=box, x="delta_wait_pct", hue="policy", hue_order=["ecokube", "carbonscaler", "keids", "topsis"], linewidth=2)
    ax.axvline(0, color="black", linewidth=0.8)
    ax.set_xlabel("Wait % change vs k8s")
    ax.set_ylabel("ECDF")
    ax.set_title("ECDF of latency deltas")
    _save(fig, out, "partial_wait_ecdf")

    # 6) Ridgeline-style KDE for carbon deltas
    rid = box[["policy", "delta_carbon_pct"]].dropna().copy()
    policies = [p for p in ["ecokube", "carbonscaler", "keids", "topsis"] if p in rid["policy"].unique()]
    fig, ax = plt.subplots(figsize=(8.4, 5.2))
    y_gap = 1.2
    for idx, pol in enumerate(policies):
        vals = rid.loc[rid["policy"] == pol, "delta_carbon_pct"].values
        if len(vals) < 2:
            continue
        kde = sns.kdeplot(x=vals, fill=True, alpha=0.35, linewidth=1.2, ax=ax, label=pol)
        # shift latest artist vertically to mimic ridgeline
        line = ax.lines[-1]
        x = line.get_xdata()
        y = line.get_ydata() + idx * y_gap
        line.set_data(x, y)
        if ax.collections:
            coll = ax.collections[-1]
            try:
                paths = coll.get_paths()
                for pth in paths:
                    verts = pth.vertices
                    verts[:, 1] += idx * y_gap
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
    scen["scenario"] = (
        "ci=" + scen["ci_weight"].astype(str)
        + "|bs=" + scen["batch_size"].astype(str)
        + "|ar=" + scen["arrival_rate"].astype(str)
        + "|r=" + scen["rep"].astype(str)
    )
    fig = plt.figure(figsize=(10.2, 4.6))
    ax = sns.stripplot(
        data=scen,
        x="policy",
        y="delta_carbon_pct",
        order=["ecokube", "carbonscaler", "keids", "topsis"],
        jitter=0.2,
        alpha=0.45,
        size=3,
    )
    sns.pointplot(
        data=scen,
        x="policy",
        y="delta_carbon_pct",
        order=["ecokube", "carbonscaler", "keids", "topsis"],
        estimator=np.mean,
        errorbar=("pi", 95),
        color="black",
        markers="d",
        linestyles="",
        join=False,
        ax=ax,
    )
    ax.axhline(0, color="black", linewidth=0.8)
    ax.set_xlabel("Policy")
    ax.set_ylabel("Carbon % change vs k8s")
    ax.set_title("Scenario-wise paired deltas (dots) + mean/PI")
    _save(fig, out, "partial_paired_delta_dotplot")


if __name__ == "__main__":
    main()
