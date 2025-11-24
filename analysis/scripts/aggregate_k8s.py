#!/usr/bin/env python3
"""Aggregate Kubernetes replay decision traces into KPIs and Pareto-ready plots."""

from __future__ import annotations

import argparse
import csv
import json
from dataclasses import dataclass
from datetime import datetime, timezone, timedelta
from pathlib import Path
from typing import Dict, Iterable, List, Tuple

import numpy as np
import pandas as pd
import math

try:
    import matplotlib.pyplot as plt
except ImportError:  # pragma: no cover - optional dependency
    plt = None

try:  # pragma: no cover - optional dependency
    import seaborn as sns
except ImportError:
    sns = None

DEFAULT_E_REF = 10.0  # kWh
DEFAULT_C_REF = 5.0   # kg

REPO_ROOT = Path(__file__).resolve().parents[2]
DEFAULT_FIGURES_DIR = REPO_ROOT / "analysis" / "figures" / "k8s"

POLICY_LABELS = {
    "ecokube": "EcoKube",
    "carbonscaler": "CarbonScaler",
    "k8s": "Kubernetes base",
}
POLICY_ORDER = ["k8s", "carbonscaler", "ecokube"]
POLICY_COLORS = {
    "ecokube": "#8c564b",
    "carbonscaler": "#ff7f0e",
    "k8s": "#1f77b4",
}


def _parse_time(value: str) -> datetime | None:
    if not value or value.startswith("0001-01-01"):
        return None
    if value.endswith("Z"):
        value = value.replace("Z", "+00:00")
    return datetime.fromisoformat(value).astimezone(timezone.utc)


@dataclass
class JobRecord:
    job_id: str
    site: str
    node: str
    queued_at: datetime
    started_at: datetime
    ended_at: datetime
    energy_kwh: float
    carbon_kg: float
    queue_seconds: float | None

    @property
    def wait_s(self) -> float:
        return max((self.started_at - self.queued_at).total_seconds(), 0.0)

    @property
    def runtime_s(self) -> float:
        return max((self.ended_at - self.started_at).total_seconds(), 0.0)

    @property
    def carbon_g(self) -> float:
        return self.carbon_kg * 1000.0

    @property
    def ci_cost_g(self) -> float:
        return self.carbon_g

    @property
    def energy_wh(self) -> float:
        return self.energy_kwh * 1000.0


def load_workload_durations(csv_path: Path) -> Dict[str, float]:
    durations = {}
    with csv_path.open() as handle:
        for row in csv.DictReader(handle):
            job_token = str(row["id"]).split("-")[-1]
            duration = float(row["duration"])
            durations[job_token] = duration
            trimmed = job_token.lstrip("0") or "0"
            durations[trimmed] = duration
            durations[trimmed.zfill(3)] = duration
            durations[str(int(trimmed))] = duration
    return durations


def pick_latest_records(jsonl_path: Path, policy: str) -> List[dict]:
    latest: Dict[str, dict] = {}
    if not jsonl_path.exists():
        return []
    with jsonl_path.open() as handle:
        for line in handle:
            if not line.strip():
                continue
            rec = json.loads(line)
            if rec.get("fallback"):
                continue
            job_id = rec.get("job_id")
            if not job_id:
                continue
            # keep the record with the latest ended_at (or started_at if missing)
            previous = latest.get(job_id)
            def _key(item: dict) -> tuple:
                end = _parse_time(item.get("ended_at", ""))
                start = _parse_time(item.get("started_at", ""))
                return (end or datetime.min.replace(tzinfo=timezone.utc),
                        start or datetime.min.replace(tzinfo=timezone.utc))
            if previous is None or _key(rec) >= _key(previous):
                latest[job_id] = rec
    return list(latest.values())


def build_records(rec_list: Iterable[dict],
                  durations: Dict[str, float],
                  e_ref: float,
                  c_ref: float) -> List[JobRecord]:
    results: List[JobRecord] = []
    for rec in rec_list:
        raw_id = rec.get("job_id")
        if not raw_id:
            continue
        job_id = str(raw_id)
        duration = durations.get(job_id)
        token = job_id.split("-")[-1]
        if duration is None:
            duration = durations.get(token)
            if duration is not None:
                job_id = token
        if duration is None:
            trimmed = token.lstrip("0") or "0"
            duration = durations.get(trimmed)
            if duration is not None:
                job_id = trimmed
        if duration is None:
            # Ignore traces we cannot map back to a workload row (e.g. helper pods).
            continue
        queued = _parse_time(rec.get("queued_at"))
        started = _parse_time(rec.get("started_at"))
        ended = _parse_time(rec.get("ended_at"))
        if queued is None:
            queued = datetime.now(timezone.utc)
        if started is None:
            started = queued
        if ended is None:
            ended = started + timedelta(seconds=duration)
        duration_fallback = durations.get(job_id)
        if started is None:
            started = queued
        if ended is None:
            if duration_fallback is not None:
                ended = started + timedelta(seconds=duration_fallback)
            else:
                ended = started

        energy_kwh = float(rec.get("e_norm", 0.0)) * e_ref
        carbon_kg = float(rec.get("c_norm", 0.0)) * c_ref
        if (carbon_kg == 0 or not math.isfinite(carbon_kg)) and rec.get("ci_cost") is not None:
            try:
                carbon_kg = float(rec.get("ci_cost")) / 1000.0
            except (TypeError, ValueError):
                carbon_kg = 0.0
        queue_seconds = rec.get("queue_seconds")
        results.append(
            JobRecord(
                job_id=job_id,
                site=rec.get("site", ""),
                node=rec.get("node", ""),
                queued_at=queued,
                started_at=started,
                ended_at=ended,
                energy_kwh=energy_kwh,
                carbon_kg=carbon_kg,
                queue_seconds=float(queue_seconds) if queue_seconds is not None else None,
            )
        )
    return results


def aggregate_policy(records: List[JobRecord]) -> Dict[str, float]:
    makespan = (max(r.ended_at for r in records) - min(r.queued_at for r in records)).total_seconds()
    total_carbon = sum(r.carbon_kg for r in records)
    total_energy = sum(r.energy_kwh for r in records)
    total_ci_cost_g = sum(r.ci_cost_g for r in records)
    wait = [r.wait_s for r in records]
    runtime = [r.runtime_s for r in records]
    return {
        "jobs": len(records),
        "makespan_s": makespan,
        "avg_wait_s": sum(wait) / len(wait),
        "avg_runtime_s": sum(runtime) / len(runtime),
        "total_carbon_kg": total_carbon,
        "total_ci_cost_g": total_ci_cost_g,
        "avg_carbon_g_per_job": total_ci_cost_g / len(records),
        "avg_ci_cost_g_per_job": total_ci_cost_g / len(records),
        "total_energy_kwh": total_energy,
        "total_energy_wh": total_energy * 1000.0,
        "avg_energy_kwh_per_job": total_energy / len(records),
        "avg_energy_wh_per_job": (total_energy * 1000.0) / len(records),
    }


def export_per_job(records: List[JobRecord], policy: str, out_dir: Path) -> pd.DataFrame:
    rows: List[dict] = []
    for rec in records:
        queue_seconds = rec.queue_seconds if rec.queue_seconds is not None else rec.wait_s
        submit = rec.queued_at.isoformat()
        start = rec.started_at.isoformat()
        end = rec.ended_at.isoformat()
        rows.append(
            {
                "policy": policy,
                "job_id": rec.job_id,
                "site": rec.site,
                "node": rec.node,
                "queued_at": submit,
                "started_at": start,
                "ended_at": end,
                "submit": submit,
                "start": start,
                "end": end,
                "wait_s": rec.wait_s,
                "runtime_s": rec.runtime_s,
                "queue_seconds": queue_seconds,
                "energy_kwh": rec.energy_kwh,
                "energy_wh": rec.energy_wh,
                "carbon_kg": rec.carbon_kg,
                "carbon_g": rec.carbon_g,
                "ci_cost": rec.ci_cost_g,
                "ci_cost_g": rec.ci_cost_g,
                "source": "kubernetes",
            }
        )

    df = pd.DataFrame(rows)
    if df.empty:
        return df

    out_dir.mkdir(parents=True, exist_ok=True)
    df.sort_values(["policy", "job_id"], inplace=True)
    df.to_csv(out_dir / "per_job.csv", index=False)
    return df


def export_summary(summary: Dict[str, Dict[str, float]], out_dir: Path) -> pd.DataFrame:
    if not summary:
        return pd.DataFrame()

    rows: List[dict] = []
    for policy, metrics in summary.items():
        record = {"policy": policy}
        record.update(metrics)
        makespan_hours = metrics["makespan_s"] / 3600.0 if metrics["makespan_s"] else np.nan
        if makespan_hours and makespan_hours > 0:
            record["throughput_jobs_per_hour"] = metrics["jobs"] / makespan_hours
        else:
            record["throughput_jobs_per_hour"] = np.nan
        rows.append(record)

    df = pd.DataFrame(rows)
    column_order = [
        "policy",
        "jobs",
        "makespan_s",
        "avg_wait_s",
        "avg_runtime_s",
        "total_carbon_kg",
        "total_ci_cost_g",
        "avg_carbon_g_per_job",
        "avg_ci_cost_g_per_job",
        "total_energy_kwh",
        "total_energy_wh",
        "avg_energy_kwh_per_job",
        "avg_energy_wh_per_job",
        "throughput_jobs_per_hour",
    ]
    existing_cols = [col for col in column_order if col in df.columns]
    df = df[existing_cols]

    out_dir.mkdir(parents=True, exist_ok=True)
    df.to_csv(out_dir / "summary.csv", index=False)
    return df


def _ordered_policies(policies: Iterable[str]) -> list[str]:
    values = list(policies)
    ordered: list[str] = []
    for candidate in POLICY_ORDER:
        if candidate in values:
            ordered.append(candidate)
    for candidate in sorted(values):
        if candidate not in ordered:
            ordered.append(candidate)
    return ordered


def _policy_label(policy: str) -> str:
    return POLICY_LABELS.get(policy, policy)


def _policy_color(policy: str) -> str:
    return POLICY_COLORS.get(policy, "#4c566a")


def _plot_message(path: Path, message: str) -> None:
    fig, ax = plt.subplots(figsize=(6, 4))
    ax.text(0.5, 0.5, message, ha="center", va="center")
    ax.axis("off")
    fig.savefig(path, dpi=300, bbox_inches="tight")
    plt.close(fig)


def _policy_latency_stats(per_job_df: pd.DataFrame) -> pd.DataFrame:
    if per_job_df.empty:
        return pd.DataFrame()
    stats = []
    grouped = per_job_df.groupby("policy")
    for policy, group in grouped:
        carbon = pd.to_numeric(group.get("carbon_g"), errors="coerce").dropna()
        waits = pd.to_numeric(group.get("wait_s"), errors="coerce").dropna()
        if carbon.empty or waits.empty:
            continue
        stats.append(
            {
                "policy": policy,
                "carbon_per_job_g": float(np.median(carbon)),
                "wait_p95_s": float(np.percentile(waits, 95)),
            }
        )
    return pd.DataFrame(stats)


def _plot_pareto_latency(stats: pd.DataFrame, outfile: Path) -> None:
    if stats.empty:
        _plot_message(outfile, "No per-job carbon/latency data")
        return
    stats = stats.copy()
    stats.sort_values("carbon_per_job_g", inplace=True)
    indexed = stats.set_index("policy")
    pareto_df = compute_pareto(indexed, objectives=("carbon_per_job_g", "wait_p95_s"))
    pareto_policies = set(pareto_df.index)
    ordering = _ordered_policies(stats["policy"])
    stats["policy"] = pd.Categorical(stats["policy"], categories=ordering, ordered=True)
    stats.sort_values("policy", inplace=True)

    fig, ax = plt.subplots(figsize=(7, 5))
    front_points = []
    for _, row in stats.iterrows():
        policy = str(row["policy"])
        x = row["carbon_per_job_g"]
        y = row["wait_p95_s"]
        ax.scatter(
            x,
            y,
            s=110,
            c=_policy_color(policy),
            edgecolor="black" if policy in pareto_policies else "none",
            linewidth=1.0 if policy in pareto_policies else 0.0,
            alpha=0.9,
        )
        ax.annotate(_policy_label(policy), (x, y), textcoords="offset points", xytext=(6, 4))
        if policy in pareto_policies:
            front_points.append((x, y))

    if len(front_points) > 1:
        hull = pd.DataFrame(front_points, columns=["carbon", "latency"]).sort_values(
            ["carbon", "latency"]
        )
        ax.plot(hull["carbon"], hull["latency"], color="#333333", linestyle="--", linewidth=1.2)

    ax.set_xlabel("Carbon per job (g)")
    ax.set_ylabel("Tail latency (p95, s)")
    ax.set_title("Kubernetes Pareto (carbon vs. tail latency)")
    ax.grid(alpha=0.35, linestyle=":")
    fig.tight_layout()
    fig.savefig(outfile, dpi=300, bbox_inches="tight")
    plt.close(fig)


def _plot_latency_violin(per_job_df: pd.DataFrame, outfile: Path) -> None:
    data = per_job_df.copy()
    data["wait_s"] = pd.to_numeric(data.get("wait_s"), errors="coerce")
    data = data.dropna(subset=["policy", "wait_s"])
    if data.empty:
        _plot_message(outfile, "No per-job wait data")
        return
    ordering = _ordered_policies(sorted(data["policy"].unique()))
    labels = [_policy_label(p) for p in ordering]
    palette = [_policy_color(p) for p in ordering]
    data["policy_label"] = data["policy"].map(_policy_label)
    data["policy_label"] = pd.Categorical(data["policy_label"], categories=labels, ordered=True)

    fig, ax = plt.subplots(figsize=(7, 4))
    sns.violinplot(
        data=data,
        x="policy_label",
        y="wait_s",
        order=labels,
        palette=palette,
        linewidth=0.9,
        cut=0,
        inner="box",
        ax=ax,
    )
    ax.set_xlabel("Policy")
    ax.set_ylabel("Tail latency (s)")
    ax.set_title("Kubernetes tail latency distribution")
    ax.grid(axis="y", alpha=0.3, linestyle=":")
    fig.tight_layout()
    fig.savefig(outfile, dpi=300, bbox_inches="tight")
    plt.close(fig)


def _plot_makespan_bars(summary_df: pd.DataFrame, outfile: Path) -> None:
    data = summary_df.copy()
    data = data.dropna(subset=["policy", "makespan_s"])
    if data.empty:
        _plot_message(outfile, "No makespan data")
        return
    data["makespan_min"] = data["makespan_s"] / 60.0
    ordering = _ordered_policies(data["policy"].unique())
    height = []
    labels = []
    colors = []
    for policy in ordering:
        subset = data.loc[data["policy"] == policy, "makespan_min"]
        if subset.empty:
            continue
        height.append(float(subset.iloc[0]))
        labels.append(_policy_label(policy))
        colors.append(_policy_color(policy))

    if not height:
        _plot_message(outfile, "No makespan data")
        return

    fig, ax = plt.subplots(figsize=(6, 4))
    ax.bar(labels, height, color=colors, edgecolor="black", linewidth=0.8)
    ax.set_xlabel("Policy")
    ax.set_ylabel("Makespan (min)")
    ax.set_title("Kubernetes makespan by policy")
    ax.grid(axis="y", alpha=0.3, linestyle=":")
    fig.tight_layout()
    fig.savefig(outfile, dpi=300, bbox_inches="tight")
    plt.close(fig)


def export_figures(summary_df: pd.DataFrame, per_job_df: pd.DataFrame, out_dir: Path) -> None:
    if summary_df.empty and per_job_df.empty:
        return
    if plt is None:  # pragma: no cover - convenience guard
        raise RuntimeError(
            "matplotlib is required to export figures. "
            "Install it with `pip install matplotlib` in your environment."
        )
    if sns is None:  # pragma: no cover - convenience guard
        raise RuntimeError(
            "seaborn is required to export violin plots. "
            "Install it with `pip install seaborn` in your environment."
        )

    out_dir.mkdir(parents=True, exist_ok=True)
    stats = _policy_latency_stats(per_job_df)
    _plot_pareto_latency(stats, out_dir / "k8s_pareto_carbon_latency.png")
    _plot_latency_violin(per_job_df, out_dir / "k8s_tail_latency_violin.png")
    _plot_makespan_bars(summary_df, out_dir / "k8s_makespan_bars.png")



def compute_pareto(df: pd.DataFrame, objectives: Tuple[str, str]) -> pd.DataFrame:
    cols = list(objectives)
    if df.empty:
        return df.copy()
    valid = df.dropna(subset=cols)
    if valid.empty:
        return valid

    values = valid[cols].to_numpy(dtype=float)
    n = len(valid)
    mask = np.ones(n, dtype=bool)

    for i in range(n):
        if not mask[i]:
            continue
        better = np.all(values <= values[i], axis=1) & np.any(values < values[i], axis=1)
        if better.any():
            mask[i] = False
            continue
        dominates = np.all(values[i] <= values, axis=1) & np.any(values[i] < values, axis=1)
        mask[dominates] = False
        mask[i] = True

    return valid.loc[mask]


def run(
    ecokube_path: Path | str,
    carb_path: Path | str,
    output_dir: Path | str,
    k8s_path: Path | str | None = None,
    workloads_path: Path | str = Path("kubenergysched/config/workloads.csv"),
    e_ref: float = DEFAULT_E_REF,
    c_ref: float = DEFAULT_C_REF,
    figures_dir: Path | str | None = None,
) -> Dict[str, List[dict]]:
    ecokube_path = Path(ecokube_path)
    carb_path = Path(carb_path)
    k8s_path = Path(k8s_path) if k8s_path is not None else None
    output_dir = Path(output_dir)
    workloads_path = Path(workloads_path)

    durations = load_workload_durations(workloads_path)

    summaries: Dict[str, Dict[str, float]] = {}
    combined_frames: List[pd.DataFrame] = []

    paths = [("ecokube", ecokube_path), ("carbonscaler", carb_path)]
    if k8s_path is not None:
        paths.append(("k8s", k8s_path))

    for policy, path in paths:
        recs = pick_latest_records(path, policy)
        jobs = build_records(recs, durations, e_ref, c_ref)
        if not jobs:
            print(f"[aggregate_k8s] no valid records for policy {policy} at {path}")
            continue
        per_job_df = export_per_job(jobs, policy, output_dir / policy)
        if not per_job_df.empty:
            combined_frames.append(per_job_df)
        summaries[policy] = aggregate_policy(jobs)

    summary_dir = output_dir / "summary"
    summary_df = export_summary(summaries, summary_dir)

    pareto_df = compute_pareto(summary_df, objectives=("total_ci_cost_g", "avg_wait_s"))
    if not pareto_df.empty:
        pareto_df.to_csv(summary_dir / "pareto.csv", index=False)

    combined_df = pd.concat(combined_frames, ignore_index=True) if combined_frames else pd.DataFrame()
    target_figures_dir = Path(figures_dir) if figures_dir is not None else DEFAULT_FIGURES_DIR
    export_figures(summary_df, combined_df, target_figures_dir)
    if not combined_df.empty:
        combined_df.sort_values(["policy", "job_id"], inplace=True)
        combined_df.to_csv(output_dir / "per_job_combined.csv", index=False)

    print("Wrote outputs under", output_dir.resolve())
    return {
        "summary": summary_df.to_dict("records"),
        "pareto": pareto_df.to_dict("records"),
        "per_job": combined_df.to_dict("records"),
    }


def main() -> None:
    parser = argparse.ArgumentParser(description="Aggregate Kubernetes replay decision traces.")
    parser.add_argument("--ecokube", type=Path, required=True, help="Path to ecokube decisions.jsonl")
    parser.add_argument("--carbonscaler", type=Path, required=True, help="Path to carbonscaler decisions.jsonl")
    parser.add_argument("--k8s", type=Path, default=None, help="Path to k8s decisions.jsonl (optional baseline)")
    parser.add_argument("--workloads", type=Path, default=Path("kubenergysched/config/workloads.csv"))
    parser.add_argument("--output", type=Path, default=Path("kubenergysched/results_k8s"))
    parser.add_argument("--eref", type=float, default=DEFAULT_E_REF)
    parser.add_argument("--cref", type=float, default=DEFAULT_C_REF)
    parser.add_argument(
        "--figures-dir",
        type=Path,
        default=None,
        help="Directory for PNG figures (default: analysis/figures/k8s)",
    )
    args = parser.parse_args()

    run(
        ecokube_path=args.ecokube,
        carb_path=args.carbonscaler,
        k8s_path=args.k8s,
        output_dir=args.output,
        workloads_path=args.workloads,
        e_ref=args.eref,
        c_ref=args.cref,
        figures_dir=args.figures_dir,
    )


if __name__ == "__main__":
    main()
