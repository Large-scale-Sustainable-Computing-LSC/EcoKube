#!/usr/bin/env python3
"""Generate animated placement traces for simulator/Kubernetes runs."""

from __future__ import annotations

import argparse
import json
import math
import shutil
import sys
from dataclasses import dataclass
from pathlib import Path
from typing import Dict, Iterable, List, Optional, Tuple

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt
from matplotlib import animation
import numpy as np
import pandas as pd


DEFAULT_PLOT_WIDTH = 10
DEFAULT_PLOT_HEIGHT = 6
DEFAULT_INTERVAL_MS = 400
DEFAULT_FPS = 5
CI_SMOOTH_WINDOW = 5
CI_SMOOTH_STEP_MIN = 5
DEFAULT_CARBON_INTENSITY = 400.0
DEFAULT_PEAK_POWER = 400.0
DEFAULT_IDLE_FRAC = 0.20
CPU_SCALING_GAMMA = 0.8
MIN_CI_SCALE = 0.25
MAX_CI_SCALE = 1.2


@dataclass
class SiteSpec:
    site_id: str
    pue: float = 1.0
    k: float = 1.0
    carbon_intensity: float = DEFAULT_CARBON_INTENSITY


@dataclass
class NodeSpec:
    name: str
    total_cpu: float
    ci_profile: str
    base_ci: float
    site_id: str
    metadata: Dict[str, str]
    labels: Dict[str, str]


@dataclass
class WorkloadSpec:
    job_id: str
    cpu: float
    duration_s: float
    labels: Dict[str, str]


def parse_args(argv: Optional[Iterable[str]] = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", choices={"simulation", "kubernetes"}, required=True)
    parser.add_argument("--input", required=True, help="Per-job CSV/JSONL to animate.")
    parser.add_argument(
        "--policy",
        help="Filter policy name (required for Kubernetes combined exports).",
    )
    parser.add_argument(
        "--nodes-csv",
        default="hetsched/config/nodes.csv",
        help="Simulator nodes CSV (simulation only).",
    )
    parser.add_argument(
        "--workloads-csv",
        default="hetsched/config/workloads.csv",
        help="Simulator workloads CSV (simulation only).",
    )
    parser.add_argument(
        "--sites-file",
        default="hetsched/config/sites.json",
        help="Site metadata (JSON or CSV).",
    )
    parser.add_argument(
        "--limit",
        type=int,
        default=None,
        help="Limit number of placements animated.",
    )
    parser.add_argument(
        "--interval-ms",
        type=int,
        default=DEFAULT_INTERVAL_MS,
        help="Delay between frames (milliseconds).",
    )
    parser.add_argument(
        "--fps",
        type=int,
        default=DEFAULT_FPS,
        help="Frames per second for the writer.",
    )
    parser.add_argument(
        "--title",
        default=None,
        help="Custom plot title (defaults to backend/policy).",
    )
    parser.add_argument(
        "--output",
        required=True,
        help="Output animation path (gif/mp4).",
    )
    return parser.parse_args(argv)


def load_sites(path: Path) -> Dict[str, SiteSpec]:
    if not path.exists():
        raise FileNotFoundError(path)
    if path.suffix.lower() == ".json":
        data = json.loads(path.read_text(encoding="utf-8"))
        sites = {}
        for key, payload in data.items():
            sites[str(key)] = SiteSpec(
                site_id=str(key),
                pue=float(payload.get("pue", 1.0)),
                k=float(payload.get("k", 1.0)),
                carbon_intensity=float(payload.get("ci", DEFAULT_CARBON_INTENSITY)),
            )
        return sites

    df = pd.read_csv(path)
    if "id" not in df.columns:
        raise ValueError(f"{path} lacks 'id' column")
    sites = {}
    for _, row in df.iterrows():
        site_id = str(row["id"])
        sites[site_id] = SiteSpec(
            site_id=site_id,
            pue=float(row.get("pue", 1.0) or 1.0),
            k=float(row.get("k", 1.0) or 1.0),
            carbon_intensity=float(
                row.get("ci", row.get("carbon_intensity", DEFAULT_CARBON_INTENSITY))
                or DEFAULT_CARBON_INTENSITY
            ),
        )
    return sites


def _parse_labels(raw: str) -> Dict[str, str]:
    labels: Dict[str, str] = {}
    if not isinstance(raw, str):
        return labels
    for token in raw.split(","):
        token = token.strip()
        if not token:
            continue
        if "=" in token:
            key, value = token.split("=", 1)
            labels[key.strip()] = value.strip()
        else:
            labels[token] = "true"
    return labels


def _base_ci_from_profile(profile: str) -> float:
    if not profile:
        return float("nan")
    parts = profile.split(":")
    mode = parts[0]
    try:
        if mode == "static" and len(parts) >= 2:
            return float(parts[1])
        if mode == "sine" and len(parts) >= 2:
            return float(parts[1])
        if mode == "randwalk" and len(parts) >= 3:
            return (float(parts[1]) + float(parts[2])) / 2.0
    except ValueError:
        return float("nan")
    return float("nan")


def load_nodes(path: Path) -> Dict[str, NodeSpec]:
    if not path.exists():
        raise FileNotFoundError(path)
    df = pd.read_csv(path)
    required = {"name", "cpu", "ci_profile"}
    if not required <= set(df.columns):
        missing = required - set(df.columns)
        raise ValueError(f"{path} missing columns: {missing}")
    nodes: Dict[str, NodeSpec] = {}
    for _, row in df.iterrows():
        name = str(row["name"])
        metadata = {"ci_profile": str(row.get("ci_profile", "")).strip()}
        labels = _parse_labels(row.get("labels", ""))
        peak = row.get("peak_power_w")
        if pd.notna(peak):
            metadata["peak_power_w"] = str(peak)
        nodes[name] = NodeSpec(
            name=name,
            total_cpu=float(row.get("cpu", 0.0) or 0.0),
            ci_profile=metadata["ci_profile"],
            base_ci=_base_ci_from_profile(metadata["ci_profile"]),
            site_id=str(row.get("site", "")).strip(),
            metadata=metadata,
            labels=labels,
        )
    return nodes


def load_workloads(path: Path) -> Dict[str, WorkloadSpec]:
    if not path.exists():
        raise FileNotFoundError(path)
    df = pd.read_csv(path)
    required = {"id", "cpu", "duration"}
    if not required <= set(df.columns):
        missing = required - set(df.columns)
        raise ValueError(f"{path} missing columns: {missing}")
    workloads: Dict[str, WorkloadSpec] = {}
    for _, row in df.iterrows():
        labels: Dict[str, str] = {}
        preferred = str(row.get("preferred_site", "")).strip()
        if preferred:
            labels["preferred_site"] = preferred
        resource_class = str(row.get("resource_class", "")).strip()
        if resource_class:
            labels["resource_class"] = resource_class
        gpu_count = row.get("gpu_count")
        if pd.notna(gpu_count) and float(gpu_count) > 0:
            labels["requires_gpu"] = "true"
            labels["gpu_count"] = str(int(gpu_count))
        workloads[str(row["id"])] = WorkloadSpec(
            job_id=str(row["id"]),
            cpu=float(row.get("cpu", 0.0) or 0.0),
            duration_s=float(row.get("duration", 0.0) or 0.0),
            labels=labels,
        )
    return workloads


def clamp(value: float, min_v: float, max_v: float) -> float:
    return max(min_v, min(max_v, value))


def _raw_ci(node: NodeSpec, sample_ts: pd.Timestamp, site: Optional[SiteSpec]) -> float:
    profile = node.ci_profile or node.labels.get("ci_profile", "")
    if not profile:
        if site and site.carbon_intensity > 0:
            return site.carbon_intensity
        if not math.isnan(node.base_ci):
            return node.base_ci
        return DEFAULT_CARBON_INTENSITY
    parts = profile.split(":")
    mode = parts[0]
    try:
        if mode == "static" and len(parts) >= 2:
            return float(parts[1])
        if mode == "sine" and len(parts) >= 4:
            mean = float(parts[1])
            amp = float(parts[2])
            period = float(parts[3])
            if period <= 0:
                return mean
            theta = 2 * math.pi * ((sample_ts.timestamp()) % period) / period
            return mean + amp * math.sin(theta)
        if mode == "randwalk" and len(parts) >= 3:
            min_v = float(parts[1])
            max_v = float(parts[2])
            if max_v <= min_v:
                return min_v
            step = float(parts[3]) if len(parts) >= 4 else 60.0
            if step <= 0:
                step = 60.0
            frac = (sample_ts.timestamp() / step) % 1.0
            return min_v + (max_v - min_v) * frac
    except ValueError:
        pass
    if site and site.carbon_intensity > 0:
        return site.carbon_intensity
    if not math.isnan(node.base_ci):
        return node.base_ci
    return DEFAULT_CARBON_INTENSITY


def current_ci(node: NodeSpec, start_ts: pd.Timestamp, site: Optional[SiteSpec]) -> float:
    if CI_SMOOTH_WINDOW <= 1:
        return _raw_ci(node, start_ts, site)
    step = pd.Timedelta(minutes=CI_SMOOTH_STEP_MIN)
    total = 0.0
    samples = 0
    for idx in range(CI_SMOOTH_WINDOW):
        total += _raw_ci(node, start_ts - idx * step, site)
        samples += 1
    if samples == 0:
        return _raw_ci(node, start_ts, site)
    value = total / samples
    return max(0.0, value)


def compute_energy_and_carbon(
    node: NodeSpec,
    job: WorkloadSpec,
    start_ts: pd.Timestamp,
    sites: Dict[str, SiteSpec],
) -> Tuple[float, float]:
    site = sites.get(node.site_id)
    ci = current_ci(node, start_ts, site)
    if ci <= 0:
        if site and site.carbon_intensity > 0:
            ci = site.carbon_intensity
        elif not math.isnan(node.base_ci):
            ci = node.base_ci
        else:
            ci = DEFAULT_CARBON_INTENSITY

    peak = float(node.metadata.get("peak_power_w", DEFAULT_PEAK_POWER))
    idle_frac = float(node.metadata.get("idle_power_fraction", DEFAULT_IDLE_FRAC))
    idle_frac = clamp(idle_frac, 0.0, 1.0)

    cpu_frac = 0.0
    if node.total_cpu > 0:
        cpu_frac = job.cpu / node.total_cpu
    cpu_frac = clamp(cpu_frac, 0.0, 1.0)
    if cpu_frac > 0:
        cpu_frac = cpu_frac ** CPU_SCALING_GAMMA

    dynamic = max(peak - peak * idle_frac, 0.0)
    power_w = peak * idle_frac + cpu_frac * dynamic

    duration_hours = job.duration_s / 3600.0
    if duration_hours <= 0:
        duration_hours = 1.0 / 3600.0

    energy_kwh = (power_w / 1000.0) * duration_hours
    if site and site.k > 0:
        energy_kwh *= site.k

    if ci > 0:
        scale = clamp(ci / DEFAULT_CARBON_INTENSITY, MIN_CI_SCALE, MAX_CI_SCALE)
        energy_kwh *= scale

    pue = site.pue if site and site.pue > 0 else 1.0
    carbon_kg = energy_kwh * pue * (ci / 1000.0)
    preferred = job.labels.get("preferred_site")
    if preferred and node.site_id and preferred != node.site_id:
        carbon_kg *= 1.25
    return energy_kwh, carbon_kg * 1000.0


def _parse_datetime(series: pd.Series, fallback: Optional[pd.Series] = None) -> pd.Series:
    parsed = pd.to_datetime(series, utc=True, errors="coerce")
    if fallback is not None:
        parsed = parsed.fillna(pd.to_datetime(fallback, utc=True, errors="coerce"))
    return parsed


def load_simulation_frame(args: argparse.Namespace) -> pd.DataFrame:
    nodes = load_nodes(Path(args.nodes_csv))
    workloads = load_workloads(Path(args.workloads_csv))
    sites = load_sites(Path(args.sites_file))
    df = pd.read_csv(args.input)
    for column in ("submit", "start", "end"):
        if column in df.columns:
            df[column] = _parse_datetime(df[column])
    if "start" not in df.columns:
        df["start"] = df["submit"]
    energy = []
    carbon = []
    for _, row in df.iterrows():
        node = nodes.get(str(row.get("node", "")))
        job = workloads.get(str(row.get("job_id", "")))
        start_ts = row.get("start")
        if pd.isna(start_ts):
            start_ts = row.get("submit")
        if pd.isna(start_ts):
            start_ts = pd.Timestamp.utcnow()
        if node is None or job is None:
            energy.append(np.nan)
            carbon.append(row.get("ci_cost", np.nan))
            continue
        e_kwh, carbon_g = compute_energy_and_carbon(node, job, start_ts, sites)
        energy.append(e_kwh)
        carbon.append(carbon_g)
    df["energy_kwh"] = energy
    if "ci_cost" not in df.columns:
        df["ci_cost"] = carbon
    df["site"] = df.get("site", "").fillna("unknown")
    return df


def load_kubernetes_frame(args: argparse.Namespace) -> pd.DataFrame:
    df = pd.read_csv(args.input)
    if args.policy:
        df = df[df["policy"].str.lower() == args.policy.lower()]
    if df.empty:
        raise ValueError("No rows left after filtering by policy; check --policy.")
    for column in ("submit", "start", "end", "queued_at", "started_at", "ended_at"):
        if column in df.columns:
            df[column] = _parse_datetime(df[column])
    if "start" not in df.columns:
        df["start"] = df.get("started_at")
    df["energy_kwh"] = pd.to_numeric(df.get("energy_kwh"), errors="coerce")
    missing_energy = df["energy_kwh"].isna()
    if missing_energy.any() and "energy_wh" in df.columns:
        df.loc[missing_energy, "energy_kwh"] = (
            pd.to_numeric(df.loc[missing_energy, "energy_wh"], errors="coerce") / 1000.0
        )
    df["energy_kwh"] = df["energy_kwh"].fillna(0.0)
    df["site"] = df.get("site", "").fillna("unknown").astype(str)
    return df


def build_animation(
    df: pd.DataFrame,
    title: str,
    output_path: Path,
    interval_ms: int,
    fps: int,
) -> None:
    df = df.sort_values("start").reset_index(drop=True)
    if "site" not in df.columns:
        df["site"] = "unknown"
    sites = sorted(df["site"].fillna("unknown").unique())
    counts = np.zeros(len(sites), dtype=float)
    energy_cumsum = 0.0

    fig, ax = plt.subplots(figsize=(DEFAULT_PLOT_WIDTH, DEFAULT_PLOT_HEIGHT))
    plt.subplots_adjust(right=0.7)
    ax.set_title(title)
    bars = ax.barh(sites, counts, color="#4c72b0")
    ax.set_xlim(0, max(1, df.shape[0]))
    ax.set_xlabel("Jobs placed")

    value_labels = []
    for bar in bars:
        y = bar.get_y() + bar.get_height() / 2
        txt = ax.text(0.01, y, "0", va="center", ha="left", color="#222")
        value_labels.append(txt)

    energy_text = fig.text(0.72, 0.75, "", fontsize=14, ha="left", va="center")
    job_text = fig.text(0.72, 0.65, "", fontsize=12, ha="left", va="center")

    site_lookup = {site: idx for idx, site in enumerate(sites)}

    def update(frame_idx: int):
        nonlocal energy_cumsum
        row = df.iloc[frame_idx]
        site = row.get("site", "unknown") or "unknown"
        idx = site_lookup.get(site)
        if idx is None:
            return bars
        counts[idx] += 1
        bars[idx].set_width(counts[idx])
        value_labels[idx].set_text(f"{int(counts[idx])}")
        value_labels[idx].set_x(counts[idx] + 0.1)
        energy_cumsum += float(row.get("energy_kwh", 0.0) or 0.0)
        ax.set_xlim(0, max(ax.get_xlim()[1], counts.max() + 1))
        energy_text.set_text(f"Cumulative energy\n{energy_cumsum:.2f} kWh")
        ts = row.get("start")
        ts_text = ts.isoformat() if isinstance(ts, pd.Timestamp) else "unknown"
        job_text.set_text(f"Job {row.get('job_id', '?')} @ {site}\n{ts_text}")
        return bars

    anim = animation.FuncAnimation(
        fig,
        update,
        frames=len(df),
        interval=interval_ms,
        blit=False,
        repeat=False,
    )
    output_path.parent.mkdir(parents=True, exist_ok=True)
    writer: animation.AbstractMovieWriter
    if output_path.suffix.lower() == ".gif":
        writer = animation.PillowWriter(fps=fps)
    else:
        if shutil.which("ffmpeg"):
            writer = animation.FFMpegWriter(fps=fps)
        else:
            writer = animation.PillowWriter(fps=fps)
    anim.save(output_path, writer=writer)
    plt.close(fig)


def main(argv: Optional[Iterable[str]] = None) -> None:
    args = parse_args(argv)
    output_path = Path(args.output)
    if args.source == "simulation":
        df = load_simulation_frame(args)
    else:
        df = load_kubernetes_frame(args)
    if args.limit:
        df = df.head(args.limit)
    if df.empty:
        raise ValueError("Input dataset has no rows to animate.")
    title = args.title or f"{args.source.title()} placements"
    build_animation(df, title, output_path, args.interval_ms, args.fps)
    print(f"[animate] wrote {output_path}")


if __name__ == "__main__":
    try:
        main()
    except Exception as exc:  # pragma: no cover - CLI helper
        print(f"ERROR: {exc}", file=sys.stderr)
        sys.exit(1)
