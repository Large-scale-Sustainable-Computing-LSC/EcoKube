#!/usr/bin/env python3
from __future__ import annotations

import argparse
from pathlib import Path

import matplotlib.pyplot as plt
import pandas as pd
import seaborn as sns


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

    plt.figure(figsize=(8, 4.2))
    ax = sns.barplot(data=long, x="policy", y="delta_pct", hue="metric", order=order)
    ax.axhline(0, color="black", linewidth=0.8)
    ax.set_xlabel("Policy")
    ax.set_ylabel("Mean % change vs k8s")
    ax.set_title("Partial run headline deltas (jobs=900, arrival=0.8/1.1)")
    plt.tight_layout()
    plt.savefig(out / "partial_headline_deltas.png")
    plt.savefig(out / "partial_headline_deltas.pdf")
    plt.close()

    # 2) Pareto scatter: mean makespan vs mean carbon
    pareto = (
        d.groupby(["policy", "ci_weight", "batch_size", "arrival_rate"], as_index=False)[["total_ci_cost_g", "makespan_s"]]
        .mean()
    )
    plt.figure(figsize=(7.2, 4.8))
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
    ax.set_title("Pareto view on completed scenarios (partial)")
    plt.tight_layout()
    plt.savefig(out / "partial_pareto_scatter.png")
    plt.savefig(out / "partial_pareto_scatter.pdf")
    plt.close()

    # 3) Robustness boxplot for carbon delta
    box = m[m["policy"] != "k8s"].copy()
    plt.figure(figsize=(7.2, 4.4))
    ax = sns.boxplot(data=box, x="policy", y="delta_carbon_pct", order=["ecokube", "carbonscaler", "keids", "topsis"])
    ax.axhline(0, color="black", linewidth=0.8)
    ax.set_xlabel("Policy")
    ax.set_ylabel("Carbon % change vs k8s")
    ax.set_title("Carbon robustness across completed scenarios")
    plt.tight_layout()
    plt.savefig(out / "partial_carbon_robustness.png")
    plt.savefig(out / "partial_carbon_robustness.pdf")
    plt.close()


if __name__ == "__main__":
    main()
