#!/usr/bin/env python3
from __future__ import annotations

import argparse
import csv
from pathlib import Path


METRICS = ["total_ci_cost_g", "makespan_s", "avg_wait_s"]


def load_rows(path: Path) -> list[dict[str, str]]:
    with path.open() as f:
        return list(csv.DictReader(f))


def mean(vals: list[float]) -> float:
    return sum(vals) / len(vals) if vals else float("nan")


def main() -> None:
    ap = argparse.ArgumentParser(description="Generate aggregate relative deltas vs k8s + raw means CSVs")
    ap.add_argument("--summary", required=True, help="Path to summary.csv")
    ap.add_argument("--out-dir", required=True, help="Output directory")
    args = ap.parse_args()

    summary_path = Path(args.summary)
    out_dir = Path(args.out_dir)
    out_dir.mkdir(parents=True, exist_ok=True)

    rows = load_rows(summary_path)
    if not rows:
        raise ValueError("summary CSV is empty")

    by_policy: dict[str, dict[str, list[float]]] = {}
    for r in rows:
        p = r["policy"]
        if p not in by_policy:
            by_policy[p] = {m: [] for m in METRICS}
        for m in METRICS:
            by_policy[p][m].append(float(r[m]))

    means = {p: {m: mean(vs) for m, vs in metric_map.items()} for p, metric_map in by_policy.items()}

    if "k8s" not in means:
        raise ValueError("k8s baseline not present")
    base = means["k8s"]

    order = [p for p in ["ecokube", "topsis", "keids"] if p in means]
    order += [p for p in sorted(means.keys()) if p not in order and p != "k8s"]

    rel_precise = out_dir / "aggregate_relative_deltas_vs_k8s_precise.csv"
    rel_pretty = out_dir / "aggregate_relative_deltas_vs_k8s.csv"
    raw_means = out_dir / "aggregate_policy_means.csv"

    with rel_precise.open("w", newline="") as f:
        w = csv.writer(f)
        w.writerow([
            "policy",
            "carbon_proxy_delta_pct_vs_k8s",
            "makespan_delta_pct_vs_k8s",
            "avg_wait_delta_pct_vs_k8s",
        ])
        for p in order:
            m = means[p]
            c = 100.0 * (m["total_ci_cost_g"] - base["total_ci_cost_g"]) / base["total_ci_cost_g"]
            mk = 100.0 * (m["makespan_s"] - base["makespan_s"]) / base["makespan_s"]
            wt = 100.0 * (m["avg_wait_s"] - base["avg_wait_s"]) / base["avg_wait_s"]
            w.writerow([p, f"{c:.10f}", f"{mk:.10f}", f"{wt:.10f}"])

    with rel_pretty.open("w", newline="") as f:
        w = csv.writer(f)
        w.writerow([
            "policy",
            "carbon_proxy_delta_pct_vs_k8s",
            "makespan_delta_pct_vs_k8s",
            "avg_wait_delta_pct_vs_k8s",
        ])
        for p in order:
            m = means[p]
            c = 100.0 * (m["total_ci_cost_g"] - base["total_ci_cost_g"]) / base["total_ci_cost_g"]
            mk = 100.0 * (m["makespan_s"] - base["makespan_s"]) / base["makespan_s"]
            wt = 100.0 * (m["avg_wait_s"] - base["avg_wait_s"]) / base["avg_wait_s"]
            w.writerow([p, f"{c:+.2f}", f"{mk:+.2f}", f"{wt:+.2f}"])

    with raw_means.open("w", newline="") as f:
        w = csv.writer(f)
        w.writerow(["policy", "mean_total_ci_cost_g", "mean_makespan_s", "mean_avg_wait_s"])
        for p in sorted(means.keys(), key=lambda pol: means[pol]["total_ci_cost_g"]):
            m = means[p]
            w.writerow([p, f"{m['total_ci_cost_g']:.10f}", f"{m['makespan_s']:.10f}", f"{m['avg_wait_s']:.10f}"])

    print(rel_pretty)
    print(rel_precise)
    print(raw_means)


if __name__ == "__main__":
    main()
