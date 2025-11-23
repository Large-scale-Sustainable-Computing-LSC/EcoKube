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

DEFAULT_E_REF = 10.0  # kWh
DEFAULT_C_REF = 5.0   # kg


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


def export_figures(summary_df: pd.DataFrame, out_dir: Path) -> None:
    if summary_df.empty:
        return
    if plt is None:  # pragma: no cover - convenience guard
        raise RuntimeError(
            "matplotlib is required to export figures. "
            "Install it with `pip install matplotlib` in your environment."
    )

    out_dir.mkdir(parents=True, exist_ok=True)

    xcol = "total_ci_cost_g"
    ordered = summary_df.sort_values(xcol, na_position="last")

    fig, ax = plt.subplots(figsize=(8, 5))
    scatter = ax.scatter(
        ordered[xcol],
        ordered["avg_wait_s"],
        s=110,
        c="tab:blue",
        label="Kubernetes policies",
    )
    for _, row in ordered.iterrows():
        ax.annotate(
            row["policy"],
            (row[xcol], row["avg_wait_s"]),
            textcoords="offset points",
            xytext=(6, 6),
        )
    ax.set_xlabel("Total CI cost (g)")
    ax.set_ylabel("Average wait (s)")
    ax.set_title("Kubernetes Carbon vs Wait")
    ax.grid(alpha=0.35)
    ax.legend(handles=[scatter], loc="upper left")
    fig.tight_layout()
    fig.savefig(out_dir / "k8s_carbon_vs_wait.png", dpi=250)
    plt.close(fig)

    fig, ax = plt.subplots(figsize=(8, 5))
    pareto_df = compute_pareto(summary_df, objectives=(xcol, "makespan_s"))
    pareto_indices = set(pareto_df.index)

    for idx, row in ordered.iterrows():
        is_pareto = idx in pareto_indices
        color = "tab:orange" if is_pareto else "lightgray"
        ax.scatter(
            row[xcol],
            row["makespan_s"],
            s=120,
            c=color,
            edgecolor="black" if is_pareto else "none",
        )
        ax.annotate(
            row["policy"],
            (row[xcol], row["makespan_s"]),
            textcoords="offset points",
            xytext=(6, 6),
        )
    ax.set_xlabel("Total CI cost (g)")
    ax.set_ylabel("Makespan (s)")
    ax.set_title("Kubernetes Carbon vs Makespan")
    ax.grid(alpha=0.35)
    fig.tight_layout()
    fig.savefig(out_dir / "k8s_pareto_carbon_makespan.png", dpi=250)
    plt.close(fig)



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
    het_path: Path | str,
    carb_path: Path | str,
    output_dir: Path | str,
    k8s_path: Path | str | None = None,
    workloads_path: Path | str = Path("kubenergysched/config/workloads.csv"),
    e_ref: float = DEFAULT_E_REF,
    c_ref: float = DEFAULT_C_REF,
    figures_dir: Path | str | None = None,
) -> Dict[str, List[dict]]:
    het_path = Path(het_path)
    carb_path = Path(carb_path)
    k8s_path = Path(k8s_path) if k8s_path is not None else None
    output_dir = Path(output_dir)
    workloads_path = Path(workloads_path)

    durations = load_workload_durations(workloads_path)

    summaries: Dict[str, Dict[str, float]] = {}
    combined_frames: List[pd.DataFrame] = []

    paths = [("hetpolicy", het_path), ("carbonscaler", carb_path)]
    if k8s_path is not None:
        paths.append(("k8s", k8s_path))

    for policy, path in paths:
        recs = pick_latest_records(path, policy)
        jobs = build_records(recs, durations, e_ref, c_ref)
        per_job_df = export_per_job(jobs, policy, output_dir / policy)
        if not per_job_df.empty:
            combined_frames.append(per_job_df)
        summaries[policy] = aggregate_policy(jobs)

    summary_dir = output_dir / "summary"
    summary_df = export_summary(summaries, summary_dir)

    pareto_df = compute_pareto(summary_df, objectives=("total_ci_cost_g", "avg_wait_s"))
    if not pareto_df.empty:
        pareto_df.to_csv(summary_dir / "pareto.csv", index=False)

    if figures_dir is None:
        figures_dir = output_dir / "figures"
    export_figures(summary_df, Path(figures_dir))

    combined_df = pd.concat(combined_frames, ignore_index=True) if combined_frames else pd.DataFrame()
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
    parser.add_argument("--het", type=Path, required=True, help="Path to hetpolicy decisions.jsonl")
    parser.add_argument("--carbonscaler", type=Path, required=True, help="Path to carbonscaler decisions.jsonl")
    parser.add_argument("--k8s", type=Path, default=None, help="Path to k8s decisions.jsonl (optional baseline)")
    parser.add_argument("--workloads", type=Path, default=Path("kubenergysched/config/workloads.csv"))
    parser.add_argument("--output", type=Path, default=Path("kubenergysched/results_k8s"))
    parser.add_argument("--eref", type=float, default=DEFAULT_E_REF)
    parser.add_argument("--cref", type=float, default=DEFAULT_C_REF)
    parser.add_argument("--figures-dir", type=Path, default=None)
    args = parser.parse_args()

    run(
        het_path=args.het,
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
