#!/usr/bin/env python3
"""Aggregate Kubernetes replay decision traces into KPIs and Pareto-ready plots."""

from __future__ import annotations

import argparse
import csv
import json
from dataclasses import dataclass
from datetime import datetime, timezone, timedelta
from pathlib import Path
from typing import Dict, Iterable, List, Sequence, Tuple

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


def load_workload_durations(csv_path: Path) -> Dict[str, float]:
    durations = {}
    with csv_path.open() as handle:
        for row in csv.DictReader(handle):
            job_id = str(row["id"]).split("-")[-1]
            durations[job_id] = float(row["duration"])
    return durations


def pick_latest_records(jsonl_path: Path, policy: str) -> List[dict]:
    latest: Dict[str, dict] = {}
    with jsonl_path.open() as handle:
        for line in handle:
            if not line.strip():
                continue
            rec = json.loads(line)
            if rec.get("scheduler") != policy or rec.get("fallback"):
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
        job_id = rec.get("job_id")
        if not job_id or job_id not in durations:
            continue
        queued = _parse_time(rec.get("queued_at"))
        started = _parse_time(rec.get("started_at"))
        ended = _parse_time(rec.get("ended_at"))
        if queued is None:
            # skip if we cannot even determine submission
            continue
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
    wait = [r.wait_s for r in records]
    runtime = [r.runtime_s for r in records]
    return {
        "jobs": len(records),
        "makespan_s": makespan,
        "avg_wait_s": sum(wait) / len(wait),
        "avg_runtime_s": sum(runtime) / len(runtime),
        "total_carbon_kg": total_carbon,
        "avg_carbon_g_per_job": (total_carbon * 1000.0) / len(records),
        "total_energy_kwh": total_energy,
        "avg_energy_kwh_per_job": total_energy / len(records),
    }


def export_per_job(records: List[JobRecord], policy: str, out_dir: Path) -> List[dict]:
    import csv

    rows: List[dict] = []
    for rec in records:
        rows.append(
            {
                "policy": policy,
                "job_id": rec.job_id,
                "site": rec.site,
                "node": rec.node,
                "queued_at": rec.queued_at.isoformat(),
                "started_at": rec.started_at.isoformat(),
                "ended_at": rec.ended_at.isoformat(),
                "wait_s": rec.wait_s,
                "runtime_s": rec.runtime_s,
                "queue_seconds": rec.queue_seconds if rec.queue_seconds is not None else rec.wait_s,
                "energy_kwh": rec.energy_kwh,
                "carbon_kg": rec.carbon_kg,
            }
        )
    out_dir.mkdir(parents=True, exist_ok=True)
    rows.sort(key=lambda r: r["job_id"])
    with (out_dir / "per_job.csv").open("w", newline="") as handle:
        writer = csv.DictWriter(handle, fieldnames=rows[0].keys())
        writer.writeheader()
        writer.writerows(rows)
    return rows


def export_summary(summary: Dict[str, Dict[str, float]], out_dir: Path) -> List[dict]:
    import csv

    rows: List[dict] = []
    for policy, metrics in summary.items():
        row = {"policy": policy}
        row.update(metrics)
        row["throughput_jobs_per_hour"] = metrics["jobs"] / (metrics["makespan_s"] / 3600.0)
        rows.append(row)
    out_dir.mkdir(parents=True, exist_ok=True)
    with (out_dir / "summary.csv").open("w", newline="") as handle:
        writer = csv.DictWriter(handle, fieldnames=rows[0].keys())
        writer.writeheader()
        writer.writerows(rows)
    return rows


def export_pareto(summary_rows: List[dict], out_dir: Path) -> None:
    out_dir.mkdir(parents=True, exist_ok=True)
    png_path = out_dir / "k8s_pareto_carbon_makespan.png"
    if plt is None:
        render_simple_png(summary_rows, png_path)
    else:
        fig, ax = plt.subplots(figsize=(6, 5))
        for row in summary_rows:
            color = "#1f77b4" if row["policy"] == "hetpolicy" else "#ff7f0e"
            ax.scatter(row["total_carbon_kg"] * 1000.0, row["makespan_s"], s=120, c=color)
            ax.annotate(row["policy"], (row["total_carbon_kg"] * 1000.0, row["makespan_s"]), textcoords="offset points", xytext=(5, 5))
        ax.set_xlabel("Total carbon footprint (g)")
        ax.set_ylabel("Makespan (s)")
        ax.set_title("Kubernetes Replay Pareto")
        ax.grid(alpha=0.3)
        fig.tight_layout()
        fig.savefig(png_path, dpi=200)
        plt.close(fig)


def render_simple_png(summary_rows: Sequence[dict], out_path: Path, width: int = 640, height: int = 480) -> None:
    """Generate a minimal PNG scatter plot using only the standard library."""
    import struct
    import zlib

    margin = 60
    data = bytearray([255] * width * height * 4)  # RGBA

    carbons = [row["total_carbon_kg"] * 1000.0 for row in summary_rows]
    makespans = [row["makespan_s"] for row in summary_rows]

    min_c, max_c = min(carbons), max(carbons)
    min_m, max_m = min(makespans), max(makespans)
    if max_c - min_c < 1e-9:
        max_c += 1
        min_c -= 1
    if max_m - min_m < 1e-9:
        max_m += 1
        min_m -= 1

    def transform(c, m):
        x = margin + int((c - min_c) / (max_c - min_c) * (width - 2 * margin))
        y = height - margin - int((m - min_m) / (max_m - min_m) * (height - 2 * margin))
        return x, y

    def put_pixel(x, y, rgba):
        if 0 <= x < width and 0 <= y < height:
            idx = (y * width + x) * 4
            data[idx:idx + 4] = rgba

    def draw_line(x0, y0, x1, y1, rgba):
        dx = abs(x1 - x0)
        dy = -abs(y1 - y0)
        sx = 1 if x0 < x1 else -1
        sy = 1 if y0 < y1 else -1
        err = dx + dy
        while True:
            put_pixel(x0, y0, rgba)
            if x0 == x1 and y0 == y1:
                break
            e2 = 2 * err
            if e2 >= dy:
                err += dy
                x0 += sx
            if e2 <= dx:
                err += dx
                y0 += sy

    # Draw axes
    draw_line(margin, height - margin, width - margin, height - margin, (0, 0, 0, 255))
    draw_line(margin, margin, margin, height - margin, (0, 0, 0, 255))

    # Plot points
    colors = {
        "hetpolicy": (31, 119, 180, 255),
        "carbonscaler": (255, 127, 14, 255),
    }
    for row in summary_rows:
        x, y = transform(row["total_carbon_kg"] * 1000.0, row["makespan_s"])
        color = colors.get(row["policy"], (0, 0, 0, 255))
        for dx in range(-4, 5):
            for dy in range(-4, 5):
                if dx * dx + dy * dy <= 16:
                    put_pixel(x + dx, y + dy, color)

    # Simple labels near the points
    for row in summary_rows:
        x, y = transform(row["total_carbon_kg"] * 1000.0, row["makespan_s"])
        text = row["policy"]
        for i, ch in enumerate(text):
            # crude 5x7 font (ASCII) using built-in patterns
            glyph = _FONT_5x7.get(ch.upper())
            if not glyph:
                continue
            for dy, line in enumerate(glyph):
                for dx, on in enumerate(line):
                    if on == "1":
                        put_pixel(x + 8 + dx + i * 6, y - dy - 4, (0, 0, 0, 255))

    # encode PNG
    raw = bytearray()
    stride = width * 4
    for y in range(height):
        raw.append(0)
        raw.extend(data[y * stride:(y + 1) * stride])
    compressed = zlib.compress(bytes(raw), level=9)

    def chunk(tag: bytes, payload: bytes) -> bytes:
        return struct.pack(">I", len(payload)) + tag + payload + struct.pack(">I", zlib.crc32(tag + payload) & 0xffffffff)

    png = b"\x89PNG\r\n\x1a\n"
    ihdr = struct.pack(">IIBBBBB", width, height, 8, 6, 0, 0, 0)
    png += chunk(b"IHDR", ihdr)
    png += chunk(b"IDAT", compressed)
    png += chunk(b"IEND", b"")
    out_path.parent.mkdir(parents=True, exist_ok=True)
    out_path.write_bytes(png)


FONT_RAW = {
    "A": ["01110", "10001", "10001", "11111", "10001", "10001", "10001"],
    "B": ["11110", "10001", "11110", "10001", "10001", "10001", "11110"],
    "C": ["01111", "10000", "10000", "10000", "10000", "10000", "01111"],
    "E": ["11111", "10000", "11110", "10000", "10000", "10000", "11111"],
    "H": ["10001", "10001", "11111", "10001", "10001", "10001", "10001"],
    "I": ["01110", "00100", "00100", "00100", "00100", "00100", "01110"],
    "L": ["10000", "10000", "10000", "10000", "10000", "10000", "11111"],
    "N": ["10001", "11001", "10101", "10011", "10001", "10001", "10001"],
    "O": ["01110", "10001", "10001", "10001", "10001", "10001", "01110"],
    "P": ["11110", "10001", "11110", "10000", "10000", "10000", "10000"],
    "R": ["11110", "10001", "11110", "10100", "10010", "10001", "10001"],
    "S": ["01111", "10000", "10000", "01110", "00001", "00001", "11110"],
    "T": ["11111", "00100", "00100", "00100", "00100", "00100", "00100"],
    "Y": ["10001", "01010", "00100", "00100", "00100", "00100", "00100"],
}

_FONT_5x7 = {ch: rows for ch, rows in FONT_RAW.items()}


def compute_pareto(rows: Sequence[dict], objectives: Tuple[str, str]) -> List[dict]:
    obj1, obj2 = objectives
    pareto = []
    for row in rows:
        dominated = False
        for other in rows:
            if other is row:
                continue
            if (
                other[obj1] <= row[obj1]
                and other[obj2] <= row[obj2]
                and (other[obj1] < row[obj1] or other[obj2] < row[obj2])
            ):
                dominated = True
                break
        if not dominated:
            pareto.append(row.copy())
    return pareto


def run(
    het_path: Path | str,
    carb_path: Path | str,
    output_dir: Path | str,
    workloads_path: Path | str = Path("kubenergysched/config/workloads.csv"),
    e_ref: float = DEFAULT_E_REF,
    c_ref: float = DEFAULT_C_REF,
) -> Dict[str, List[dict]]:
    het_path = Path(het_path)
    carb_path = Path(carb_path)
    output_dir = Path(output_dir)
    workloads_path = Path(workloads_path)

    durations = load_workload_durations(workloads_path)

    summaries: Dict[str, Dict[str, float]] = {}
    combined_per_job: List[dict] = []

    for policy, path in (("hetpolicy", het_path), ("carbonscaler", carb_path)):
        recs = pick_latest_records(path, policy)
        jobs = build_records(recs, durations, e_ref, c_ref)
        rows = export_per_job(jobs, policy, output_dir / policy)
        combined_per_job.extend(rows)
        summaries[policy] = aggregate_policy(jobs)

    summary_dir = output_dir / "summary"
    summary_rows = export_summary(summaries, summary_dir)

    pareto_rows = compute_pareto(summary_rows, objectives=("total_carbon_kg", "makespan_s"))
    if pareto_rows:
        with (summary_dir / "pareto.csv").open("w", newline="") as handle:
            writer = csv.DictWriter(handle, fieldnames=pareto_rows[0].keys())
            writer.writeheader()
            writer.writerows(pareto_rows)

    export_pareto(summary_rows, output_dir / "figures")

    if combined_per_job:
        combined_per_job.sort(key=lambda r: (r["policy"], r["job_id"]))
        with (output_dir / "per_job_combined.csv").open("w", newline="") as handle:
            writer = csv.DictWriter(handle, fieldnames=combined_per_job[0].keys())
            writer.writeheader()
            writer.writerows(combined_per_job)

    print("Wrote outputs under", output_dir.resolve())
    return {"summary": summary_rows, "pareto": pareto_rows, "per_job": combined_per_job}


def main() -> None:
    parser = argparse.ArgumentParser(description="Aggregate Kubernetes replay decision traces.")
    parser.add_argument("--het", type=Path, required=True, help="Path to hetpolicy decisions.jsonl")
    parser.add_argument("--carbonscaler", type=Path, required=True, help="Path to carbonscaler decisions.jsonl")
    parser.add_argument("--workloads", type=Path, default=Path("kubenergysched/config/workloads.csv"))
    parser.add_argument("--output", type=Path, default=Path("kubenergysched/results_k8s"))
    parser.add_argument("--eref", type=float, default=DEFAULT_E_REF)
    parser.add_argument("--cref", type=float, default=DEFAULT_C_REF)
    args = parser.parse_args()

    run(
        het_path=args.het,
        carb_path=args.carbonscaler,
        output_dir=args.output,
        workloads_path=args.workloads,
        e_ref=args.eref,
        c_ref=args.cref,
    )


if __name__ == "__main__":
    main()
