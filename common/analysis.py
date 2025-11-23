"""Shared analysis utilities for harmonising experiment outputs.

This module centralises the data contract used by both the simulator and
Kubernetes replay notebooks.  Functions here focus on transforming the raw
CSV/JSON logs into a canonical schema, computing bootstrap statistics, and
deriving summary/effect-size tables that can be dropped straight into the
thesis assets directory.
"""

from __future__ import annotations

import json
import glob
from collections import defaultdict
from dataclasses import dataclass
from pathlib import Path
from typing import Callable, Iterable, Sequence

import numpy as np
import pandas as pd

# ---------------------------------------------------------------------------
# Canonical schema and policy normalisation


CANONICAL_COLUMNS = [
    "backend",
    "policy",
    "batch_id",
    "jobs",
    "energy_kwh",
    "carbon_gco2e",
    "makespan_min",
    "latency_p50_s",
    "latency_p95_s",
    "site",
    "assigned_jobs",
]

POLICY_ALIASES = {
    "k8": "k8s-default",
    "k8s": "k8s-default",
    "k8s_default": "k8s-default",
    "default": "k8s-default",
    "baseline": "k8s-default",
    "hetpolicy": "ecokube",
    "het-policy": "ecokube",
    "het_policy": "ecokube",
    "eco-kube": "ecokube",
    "eco_kube": "ecokube",
    "hetsched": "HetSched",
    "hetschedframework": "HetSched",
    "hetsched_framework": "HetSched",
    "themis": "HetSched",
    "themis_base": "HetSched",
    "themisbase": "HetSched",
}

# Policies appear in this order when building tables/figures; the baseline is
# still surfaced explicitly via strongest_baseline(), but the ordering helps
# keep comparisons consistent.  Policies not listed are appended alphabetically,
# with ecokube forced to the end in accordance with the brief.
POLICY_ORDER = [
    "k8s-default",
    "carbonscaler",
    "keids",
    "topsis",
    "HetSched",
    "ecokube",
]


def _normalise_policy(policy: str) -> str:
    """Map repo-specific policy identifiers onto the canonical naming."""

    if policy is None:
        raise ValueError("policy name cannot be None")
    slug = policy.strip()
    if not slug:
        raise ValueError("policy name cannot be empty")
    key = slug.lower()
    canonical = POLICY_ALIASES.get(key, key)
    # Preserve user-facing casing for canonical tags.
    return canonical


def _normalise_policy_series(series: pd.Series) -> pd.Series:
    values = []
    for value in series:
        if pd.isna(value):
            values.append(np.nan)
            continue
        text = str(value).strip()
        if not text:
            values.append(np.nan)
            continue
        try:
            values.append(_normalise_policy(text))
        except ValueError:
            values.append(text.lower())
    return pd.Series(values, index=series.index)


def _canonical_policy_sort_key(policy: str) -> tuple[int, str]:
    """Return a sort key honouring POLICY_ORDER while staying stable."""

    try:
        idx = POLICY_ORDER.index(policy)
    except ValueError:
        idx = len(POLICY_ORDER)
    # ecokube must always trail any additional policies.
    if policy == "ecokube":
        idx = max(idx, len(POLICY_ORDER))
    return (idx, policy)


# ---------------------------------------------------------------------------
# Raw log ingestion helpers


def _find_repo_root(start: Path | None = None) -> Path:
    """Locate repository root by walking up until a .git directory is found."""

    start_path = Path(start or Path.cwd()).resolve()
    for candidate in [start_path, *start_path.parents]:
        if (candidate / ".git").exists():
            return candidate
    return start_path


def _infer_batch_id(path: Path) -> str:
    """Use the relative parent path as a reproducible batch identifier."""

    try:
        repo_root = _find_repo_root()
        rel = path.parent.resolve().relative_to(repo_root)
        return str(rel).replace("\\", "/")
    except ValueError:
        # Path not underneath the current working directory; fall back to name.
        return path.parent.name


def _to_datetime(series: pd.Series) -> pd.Series:
    """Best-effort conversion for timestamp columns."""

    if series.empty:
        return series
    return pd.to_datetime(series, utc=True, errors="coerce")


def _extract_wait_seconds(df: pd.DataFrame) -> pd.Series:
    """Return wait/latency in seconds from whichever column is present."""

    for col in ("wait_s", "latency_s", "queue_seconds"):
        if col in df.columns:
            return pd.to_numeric(df[col], errors="coerce")
    if "wait_ms" in df.columns:
        return pd.to_numeric(df["wait_ms"], errors="coerce") / 1000.0
    if "wait" in df.columns:
        return pd.to_numeric(df["wait"], errors="coerce")
    return pd.Series(dtype=float)


def _extract_energy_kwh(df: pd.DataFrame) -> float:
    """Sum energy usage across the batch if per-job values are available."""

    if "energy_kwh" in df.columns:
        return pd.to_numeric(df["energy_kwh"], errors="coerce").sum()
    if "energy_wh" in df.columns:
        energy_wh = pd.to_numeric(df["energy_wh"], errors="coerce").sum()
        return energy_wh / 1000.0
    return np.nan


def _extract_carbon_g(df: pd.DataFrame) -> float:
    """Sum carbon usage across the batch if per-job values are available."""

    for col in ("carbon_g", "ci_cost_g", "total_ci_cost_g"):
        if col in df.columns:
            return pd.to_numeric(df[col], errors="coerce").sum()
    if "carbon_kg" in df.columns:
        carbon_kg = pd.to_numeric(df["carbon_kg"], errors="coerce").sum()
        return carbon_kg * 1000.0
    if "ci_cost" in df.columns:
        # Treat ci_cost as already in grams if the dataset omits _g suffix.
        return pd.to_numeric(df["ci_cost"], errors="coerce").sum()
    return np.nan


def _compute_makespan_minutes(df: pd.DataFrame) -> float:
    """Derive makespan from per-job timestamps when summary data is missing."""

    candidates = []
    for start_col in ("submit", "queued_at", "queued"):
        if start_col in df.columns:
            candidates.append(_to_datetime(df[start_col]))
    for end_col in ("end", "ended_at", "finished_at"):
        if end_col in df.columns:
            candidates.append(_to_datetime(df[end_col]))
    if len(candidates) < 2:
        return np.nan
    start = candidates[0]
    finish = candidates[-1]
    if start.empty or finish.empty:
        return np.nan
    start_min = start.min()
    finish_max = finish.max()
    if pd.isna(start_min) or pd.isna(finish_max):
        return np.nan
    makespan = (finish_max - start_min).total_seconds() / 60.0
    return float(makespan)


@dataclass
class _RunBucket:
    """Aggregation bucket for a (batch_id, policy) pair."""

    batch_id: str
    policy: str
    jobs: list[int]
    energy_kwh: list[float]
    carbon_gco2e: list[float]
    makespan_min: list[float]
    latency_p50_s: list[float]
    latency_p95_s: list[float]

    def as_record(self) -> dict:
        record = {
            "batch_id": self.batch_id,
            "policy": self.policy,
            "jobs": np.nan if not self.jobs else int(np.nanmax(self.jobs)),
            "energy_kwh": (
                np.nan if not self.energy_kwh else float(np.nanmax(self.energy_kwh))
            ),
            "carbon_gco2e": (
                np.nan if not self.carbon_gco2e else float(np.nanmax(self.carbon_gco2e))
            ),
            "makespan_min": (
                np.nan if not self.makespan_min else float(np.nanmax(self.makespan_min))
            ),
            "latency_p50_s": (
                np.nan if not self.latency_p50_s else float(np.nanmax(self.latency_p50_s))
            ),
            "latency_p95_s": (
                np.nan if not self.latency_p95_s else float(np.nanmax(self.latency_p95_s))
            ),
        }
        return record


def _update_bucket_from_summary(bucket: _RunBucket, row: pd.Series) -> None:
    """Populate aggregate statistics from summary rows."""

    jobs = row.get("num_jobs") or row.get("job_count") or row.get("jobs")
    if pd.notna(jobs):
        bucket.jobs.append(int(jobs))

    energy = (
        row.get("total_energy_kwh")
        or (row.get("total_energy_wh") / 1000.0 if pd.notna(row.get("total_energy_wh")) else None)
    )
    if pd.notna(energy):
        bucket.energy_kwh.append(float(energy))

    carbon = row.get("total_ci_cost_g")
    if pd.isna(carbon):
        carbon_kg = row.get("total_carbon_kg")
        if pd.notna(carbon_kg):
            carbon = float(carbon_kg) * 1000.0
    if pd.notna(carbon):
        bucket.carbon_gco2e.append(float(carbon))

    makespan = row.get("makespan_min")
    if pd.notna(makespan):
        bucket.makespan_min.append(float(makespan))
    makespan_seconds = row.get("makespan_s")
    if pd.notna(makespan_seconds):
        bucket.makespan_min.append(float(makespan_seconds) / 60.0)


def _process_per_job_frame(
    df: pd.DataFrame,
    batch_id: str,
    bucket_lookup: dict[tuple[str, str], _RunBucket],
    site_rows: list[dict],
) -> None:
    """Aggregate latency/energy/carbon metrics from per-job data frames."""

    if "policy" in df.columns:
        policies = df["policy"].astype(str).str.strip().unique()
        for policy in policies:
            subset = df[df["policy"].astype(str).str.strip() == policy]
            _process_per_job_subset(subset, batch_id, policy, bucket_lookup, site_rows)
    elif "sched" in df.columns:
        policies = df["sched"].astype(str).str.strip().unique()
        for policy in policies:
            subset = df[df["sched"].astype(str).str.strip() == policy]
            _process_per_job_subset(subset, batch_id, policy, bucket_lookup, site_rows)
    else:
        # Without policy metadata we cannot assign the data to a policy.
        return


def _process_per_job_subset(
    subset: pd.DataFrame,
    batch_id: str,
    policy: str,
    bucket_lookup: dict[tuple[str, str], _RunBucket],
    site_rows: list[dict],
) -> None:
    """Compute derived metrics for a single policy slice."""

    try:
        policy = _normalise_policy(policy)
    except ValueError:
        return
    key = (batch_id, policy)
    bucket = bucket_lookup.setdefault(
        key,
        _RunBucket(
            batch_id=batch_id,
            policy=policy,
            jobs=[],
            energy_kwh=[],
            carbon_gco2e=[],
            makespan_min=[],
            latency_p50_s=[],
            latency_p95_s=[],
        ),
    )

    subset = subset.copy()

    # Jobs
    bucket.jobs.append(len(subset.index))

    # Latencies
    wait_seconds = _extract_wait_seconds(subset).dropna()
    if not wait_seconds.empty:
        bucket.latency_p50_s.append(float(np.percentile(wait_seconds, 50)))
        bucket.latency_p95_s.append(float(np.percentile(wait_seconds, 95)))

    # Energy / Carbon
    energy = _extract_energy_kwh(subset)
    if pd.notna(energy):
        bucket.energy_kwh.append(float(energy))
    carbon = _extract_carbon_g(subset)
    if pd.notna(carbon):
        bucket.carbon_gco2e.append(float(carbon))

    # Makespan
    makespan = _compute_makespan_minutes(subset)
    if pd.notna(makespan):
        bucket.makespan_min.append(float(makespan))

    # Site distribution
    if "site" in subset.columns and subset["site"].notna().any():
        site_counts = (
            subset["site"]
            .astype(str)
            .replace({"": np.nan, "nan": np.nan})
            .dropna()
            .value_counts()
        )
        for site, count in site_counts.items():
            site_rows.append(
                {
                    "batch_id": batch_id,
                    "policy": policy,
                    "site": site,
                    "assigned_jobs": int(count),
                }
            )


def load_and_harmonise(glob_paths: Sequence[str]) -> pd.DataFrame:
    """Load simulator / k8s result artefacts and project onto the schema.

    Parameters
    ----------
    glob_paths:
        Sequence of glob strings.  Each pattern is evaluated relative to the
        current working directory and may match CSV/JSON/JSONL inputs.

    Returns
    -------
    pandas.DataFrame
        Wide dataframe adhering to the canonical schema, with per-policy rows.
        Site-level rows (for stacked bars) include the optional ``site`` and
        ``assigned_jobs`` columns; these can be selected via ``df.dropna`` in
        downstream notebooks.
    """

    repo_root = _find_repo_root()
    matched_files: list[Path] = []
    for pattern in glob_paths:
        if Path(pattern).is_absolute():
            matched_files.extend(Path(p) for p in glob.glob(pattern, recursive=True))
        else:
            full_pattern = str(repo_root / pattern)
            matched_files.extend(Path(p) for p in glob.glob(full_pattern, recursive=True))

    buckets: dict[tuple[str, str], _RunBucket] = {}
    site_rows: list[dict] = []

    for path in sorted(set(matched_files)):
        if not path.exists() or not path.is_file():
            continue
        suffix = path.suffix.lower()
        batch_id = _infer_batch_id(path)

        if suffix == ".csv" and path.name.lower() == "summary.csv":
            df = pd.read_csv(path)
            for _, row in df.iterrows():
                try:
                    policy = _normalise_policy(str(row.get("policy", "")))
                except ValueError:
                    continue
                key = (batch_id, policy)
                bucket = buckets.setdefault(
                    key,
                    _RunBucket(
                        batch_id=batch_id,
                        policy=policy,
                        jobs=[],
                        energy_kwh=[],
                        carbon_gco2e=[],
                        makespan_min=[],
                        latency_p50_s=[],
                        latency_p95_s=[],
                    ),
                )
                _update_bucket_from_summary(bucket, row)
            continue

        if suffix == ".json" and path.name.lower() == "summary.json":
            with open(path, "r", encoding="utf-8") as handle:
                data = json.load(handle)
            if isinstance(data, dict):
                rows = [data]
            else:
                rows = data
            df = pd.DataFrame(rows)
            for _, row in df.iterrows():
                try:
                    policy = _normalise_policy(str(row.get("policy", "")))
                except ValueError:
                    continue
                key = (batch_id, policy)
                bucket = buckets.setdefault(
                    key,
                    _RunBucket(
                        batch_id=batch_id,
                        policy=policy,
                        jobs=[],
                        energy_kwh=[],
                        carbon_gco2e=[],
                        makespan_min=[],
                        latency_p50_s=[],
                        latency_p95_s=[],
                    ),
                )
                _update_bucket_from_summary(bucket, row)
            continue

        if suffix == ".csv":
            df = pd.read_csv(path)
            _process_per_job_frame(df, batch_id, buckets, site_rows)
            continue

        if suffix in {".jsonl", ".ndjson"}:
            # JSONL decision traces can be extremely large; stream them.
            records = []
            with open(path, "r", encoding="utf-8") as handle:
                for line in handle:
                    line = line.strip()
                    if not line:
                        continue
                    try:
                        rec = json.loads(line)
                    except json.JSONDecodeError:
                        continue
                    records.append(rec)
            if records:
                df = pd.DataFrame.from_records(records)
                _process_per_job_frame(df, batch_id, buckets, site_rows)
            continue

    records: list[dict] = []
    for bucket in buckets.values():
        record = bucket.as_record()
        record["policy"] = bucket.policy
        record["batch_id"] = bucket.batch_id
        records.append(record)

    df_policy = pd.DataFrame.from_records(records)
    if df_policy.empty:
        # Return empty frame with canonical columns so downstream code is OK.
        return pd.DataFrame(columns=CANONICAL_COLUMNS)

    df_policy["policy"] = _normalise_policy_series(df_policy["policy"])
    df_policy["batch_id"] = df_policy["batch_id"].astype(str)

    frames = [df_policy]
    if site_rows:
        site_df = pd.DataFrame(site_rows)
        if not site_df.empty:
            site_df = site_df[
                site_df["policy"].notna()
                & site_df["policy"].astype(str).str.strip().ne("")
            ]
            if not site_df.empty:
                site_df["policy"] = _normalise_policy_series(site_df["policy"])
                df_site = df_policy.merge(site_df, on=["batch_id", "policy"], how="inner")
                frames.append(df_site)

    df = pd.concat(frames, ignore_index=True, sort=False)

    for column in CANONICAL_COLUMNS:
        if column not in df.columns:
            df[column] = np.nan
    df = df.reindex(columns=CANONICAL_COLUMNS)
    return df


# ---------------------------------------------------------------------------
# Statistical helpers


def strongest_baseline(df: pd.DataFrame, backend: str) -> str:
    """Return the baseline policy for the given backend.

    The baseline minimises carbon-per-job while matching the best observed
    makespan (within a numerical tolerance).  If the selection fails (e.g. the
    dataset is empty or lacks carbon/makespan), ``k8s-default`` is used if
    available, otherwise we fall back to the lexicographically first policy.
    """

    scope = df[(df.get("backend") == backend) | df["backend"].isna()]
    if scope.empty and "backend" not in df.columns:
        scope = df.copy()

    scope = scope[df.get("site").isna()] if "site" in scope.columns else scope
    if scope.empty:
        return "k8s-default"

    grouped = (
        scope.groupby("policy")[["makespan_min", "carbon_per_job_g"]]
        .median(numeric_only=True)
        .dropna(how="all")
    )
    if grouped.empty:
        policies = sorted(scope["policy"].unique(), key=_canonical_policy_sort_key)
        return policies[0] if policies else "k8s-default"

    if "makespan_min" in grouped:
        best_makespan = grouped["makespan_min"].min()
        tolerant = grouped[
            np.isclose(grouped["makespan_min"], best_makespan, rtol=1e-5, atol=1e-8)
        ]
        candidates = tolerant if "carbon_per_job_g" in tolerant else grouped
    else:
        candidates = grouped

    if "carbon_per_job_g" in candidates:
        baseline_policy = candidates["carbon_per_job_g"].idxmin()
    else:
        baseline_policy = candidates.index[0]

    return str(baseline_policy)


def bootstrap_ci(
    series: Sequence[float] | pd.Series,
    stat_fn: Callable[[np.ndarray], float],
    iters: int = 5000,
    ci: float = 0.95,
) -> tuple[float, float]:
    """Generate a percentile bootstrap confidence interval for ``stat_fn``."""

    data = pd.Series(series, dtype=float).dropna().to_numpy()
    if data.size == 0:
        return (np.nan, np.nan)
    rng = np.random.default_rng()
    stats = np.empty(iters, dtype=float)
    for idx in range(iters):
        sample = rng.choice(data, size=data.size, replace=True)
        stats[idx] = stat_fn(sample)
    alpha = (1.0 - ci) / 2.0
    lower = float(np.quantile(stats, alpha))
    upper = float(np.quantile(stats, 1.0 - alpha))
    return lower, upper


def a12(x: Sequence[float], y: Sequence[float]) -> float:
    """Vargha–Delaney A12 effect size estimator."""

    x = pd.Series(x, dtype=float).dropna().to_numpy()
    y = pd.Series(y, dtype=float).dropna().to_numpy()
    n1 = x.size
    n2 = y.size
    if n1 == 0 or n2 == 0:
        return np.nan
    ranks = pd.Series(np.concatenate([x, y])).rank(method="average").to_numpy()
    r1 = ranks[:n1].sum()
    a = (r1 / n1 - (n1 + 1) / 2) / n2 + 0.5
    return float(a)


# ---------------------------------------------------------------------------
# Summary tables and effect sizes


METRIC_LABELS = {
    "energy_kwh": "Energy (kWh)",
    "carbon_gco2e": "Carbon (gCO₂e)",
    "makespan_min": "Makespan (min)",
    "latency_p95_s": "Tail latency p95 (s)",
    "energy_per_job_kwh": "Energy per job (kWh)",
    "carbon_per_job_g": "Carbon per job (g)",
    "jobs_per_kwh": "Jobs per kWh",
}

HIGHER_IS_BETTER = {"jobs_per_kwh"}


def _format_interval(median: float, q1: float, q3: float) -> str:
    if all(np.isnan(val) for val in (median, q1, q3)):
        return ""
    return f"{median:.3g} [{q1:.3g}, {q3:.3g}]"


def _pivot_summary(stats: pd.DataFrame, policies: list[str]) -> pd.DataFrame:
    rows = []
    for metric, group in stats.groupby("metric", sort=False):
        label = METRIC_LABELS.get(metric, metric)
        row = {"metric": label}
        for policy in policies:
            match = group[group["policy"] == policy]
            row[policy] = match["formatted"].iloc[0] if not match.empty else ""
        rows.append(row)
    return pd.DataFrame(rows)


def summarise(
    df: pd.DataFrame,
    assets_dir: Path | str = "assets",
    bootstrap_iters: int = 5000,
) -> pd.DataFrame:
    """Create summary/effect-size tables and write them to ``assets_dir``."""

    if df.empty:
        return pd.DataFrame(columns=["metric"])

    if "backend" not in df.columns or df["backend"].nunique(dropna=True) != 1:
        raise ValueError("summarise() expects a single-backend dataframe.")
    backend = df["backend"].dropna().iloc[0]
    assets_path = Path(assets_dir)
    assets_path.mkdir(parents=True, exist_ok=True)

    policy_scope = df[df["site"].isna()] if "site" in df.columns else df
    metrics = list(METRIC_LABELS.keys())
    baseline_policy = strongest_baseline(df, backend)
    policies = sorted(
        policy_scope["policy"].dropna().unique(),
        key=_canonical_policy_sort_key,
    )
    if not policies:
        return pd.DataFrame(columns=["metric"])
    ordered_policies: list[str] = []
    if baseline_policy in policies and baseline_policy != "ecokube":
        ordered_policies.append(baseline_policy)
    middle = [
        policy
        for policy in policies
        if policy not in {baseline_policy, "ecokube"}
    ]
    ordered_policies.extend(middle)
    if "ecokube" in policies:
        ordered_policies.append("ecokube")
    # Include any stragglers (e.g., baseline not in policies)
    for policy in policies:
        if policy not in ordered_policies:
            ordered_policies.append(policy)
    policies = ordered_policies

    stats_rows = []
    for metric in metrics:
        grouped = policy_scope.groupby("policy")[metric]
        series = grouped.agg(
            [
                "median",
                lambda x: x.quantile(0.25),
                lambda x: x.quantile(0.75),
            ]
        ).rename(columns={"<lambda_0>": "q1", "<lambda_1>": "q3"})
        for policy, row in series.iterrows():
            stats_rows.append(
                {
                    "policy": policy,
                    "metric": metric,
                    "median": float(row.get("median", np.nan)),
                    "q1": float(row.get("q1", np.nan)),
                    "q3": float(row.get("q3", np.nan)),
                    "formatted": _format_interval(
                        float(row.get("median", np.nan)),
                        float(row.get("q1", np.nan)),
                        float(row.get("q3", np.nan)),
                    ),
                }
            )

    stats_df = pd.DataFrame(stats_rows)
    summary_table = _pivot_summary(stats_df, policies)

    # Effect sizes versus strongest baseline
    effect_rows = []

    baseline_data = {
        metric: policy_scope.loc[policy_scope["policy"] == baseline_policy, metric].dropna()
        for metric in metrics
    }

    rng = np.random.default_rng()
    for metric in metrics:
        base_series = baseline_data.get(metric, pd.Series(dtype=float))
        base_values = base_series.to_numpy()
        base_n = base_values.size

        for policy in policies:
            policy_series = policy_scope.loc[policy_scope["policy"] == policy, metric].dropna()
            policy_values = policy_series.to_numpy()
            if base_n == 0 or policy_values.size == 0:
                a_val = np.nan
                ci_low = ci_high = np.nan
            else:
                a_val = a12(policy_values, base_values)
                samples = np.empty(bootstrap_iters, dtype=float)
                for idx in range(bootstrap_iters):
                    resampled_policy = rng.choice(policy_values, size=policy_values.size, replace=True)
                    resampled_base = rng.choice(base_values, size=base_n, replace=True)
                    samples[idx] = a12(resampled_policy, resampled_base)
                alpha = 0.025
                ci_low = float(np.quantile(samples, alpha))
                ci_high = float(np.quantile(samples, 1.0 - alpha))

            effect_rows.append(
                {
                    "backend": backend,
                    "metric": METRIC_LABELS.get(metric, metric),
                    "policy": policy,
                    "baseline": baseline_policy,
                    "direction": "higher" if metric in HIGHER_IS_BETTER else "lower",
                    "A12": float(a_val) if pd.notna(a_val) else np.nan,
                    "ci_low": ci_low,
                    "ci_high": ci_high,
                }
            )

    effect_df = pd.DataFrame(effect_rows)
    effect_path = assets_path / f"{backend}_effect_sizes.csv"
    effect_df.to_csv(effect_path, index=False)

    return summary_table


def write_run_metadata(
    backend: str,
    sweep_ids: Iterable[str],
    assets_dir: Path | str = "assets",
    seed: int | None = None,
) -> Path:
    """Persist lightweight provenance so LaTeX can surface run context."""

    assets_path = Path(assets_dir)
    assets_path.mkdir(parents=True, exist_ok=True)

    git_dir = Path(".git")
    commit = "unknown"
    if git_dir.exists():
        head_path = git_dir / "HEAD"
        try:
            ref = head_path.read_text(encoding="utf-8").strip()
            if ref.startswith("ref:"):
                ref_path = git_dir / ref.split(" ", 1)[1]
                commit = ref_path.read_text(encoding="utf-8").strip()
            else:
                commit = ref
        except OSError:
            pass

    payload = {
        "backend": backend,
        "commit": commit,
        "seed": seed,
        "sweep_ids": sorted(set(sweep_ids)),
        "timestamp": pd.Timestamp.utcnow().isoformat(),
    }

    out_path = assets_path / f"{backend}_run_meta.json"
    out_path.write_text(json.dumps(payload, indent=2), encoding="utf-8")
    return out_path
