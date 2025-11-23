import csv
import os
import time
from datetime import datetime, timezone

from kubernetes import client, config

NAMESPACE = os.getenv("WORKLOAD_NS", "workloads")
IMAGE = os.getenv("JOB_IMAGE", "ubuntu:22.04")
SLEEP_BETWEEN = float(os.getenv("SUBMIT_EVERY_SEC", "0.5"))
FORCE_SITE = os.getenv("FORCE_SITE", "").strip()
TARGET_SCHEDULER_NAME = os.getenv("TARGET_SCHEDULER_NAME", "ci-aware").strip()
ENABLE_PROM = os.getenv("ENABLE_PROM_SIDECAR", "false").lower() in {"1", "true", "yes", "on"}
PROM_IMAGE = os.getenv("PROM_SIDECAR_IMAGE", "goncaloferreirauva/ciw-metrics-agent:latest")
PROM_PORT = int(os.getenv("PROM_SIDECAR_PORT", "9101"))
PROM_PATH = os.getenv("PROM_SIDECAR_PATH", "/metrics")
PROM_CPU_REQ = os.getenv("PROM_SIDECAR_CPU", "50m")
PROM_MEM_REQ = os.getenv("PROM_SIDECAR_MEMORY", "64Mi")
PROM_CPU_LIMIT = os.getenv("PROM_SIDECAR_CPU_LIMIT", "200m")
PROM_MEM_LIMIT = os.getenv("PROM_SIDECAR_MEMORY_LIMIT", "256Mi")
PROM_INTERVAL = os.getenv("PROM_SCRAPE_INTERVAL", "15s")
DEFAULT_MAX_DEFER_FRAC = float(os.getenv("DEFAULT_MAX_DEFER_FRACTION", "0"))
SCHEDULER_LABEL = os.getenv("SCHEDULER_LABEL", "baseline")
SKIP_GPU_JOBS = os.getenv("SKIP_GPU_JOBS", "false").lower() in {"1", "true", "yes", "on"}

CANDIDATE_COLS = {
    "id": ["job_id", "jobid", "task_id", "taskid", "id"],
    "cpus": ["cpus", "cpu", "num_cpu", "requested_cpu_cores", "cpu_request", "cpu_cores"],
    "mem": ["mem_gb", "memory_gb", "mem", "memory", "requested_memory_bytes", "memory_request", "ram_gb"],
    "duration": [
        "duration_s",
        "runtime_s",
        "run_time",
        "runtime_sec",
        "duration",
        "walltime_s",
        "wall_time_sec",
    ],
}
MAX_JOBS = int(os.getenv("MAX_JOBS", "50"))


def pick(row, keys, default=None):
    for key in keys:
        if key in row and row[key] not in ("", None):
            return row[key]
    return default


def parse_float(value, default=0.0):
    try:
        return float(value)
    except Exception:
        return default


def to_gi(mem_val):
    """Accept Gi, GB, bytes; return string like '4Gi'."""

    s = str(mem_val).strip().lower()
    if s.endswith("gi"):
        return s
    if s.endswith("gb") or s.endswith("g"):
        val = parse_float(s.rstrip("gb").rstrip("g"), 1.0)
        return f"{max(val, 0.25):g}Gi"
    try:
        bytes_val = float(s)
        gi = max(bytes_val / (1024 ** 3), 0.25)
        return f"{gi:g}Gi"
    except Exception:
        val = parse_float(s, 1.0)
        return f"{max(val, 0.25):g}Gi"


def parse_ts(value):
    if not value:
        return None
    value = value.strip()
    if not value:
        return None
    try:
        if value.endswith("Z"):
            value = value[:-1] + "+00:00"
        return datetime.fromisoformat(value).astimezone(timezone.utc)
    except Exception:
        return None


def job_from_row(row, index=None):
    if index is not None:
        jid = str(index).zfill(3)
    else:
        jid = str(pick(row, CANDIDATE_COLS["id"], default=f"anon-{int(time.time() * 1000)}"))

    cpus = pick(row, CANDIDATE_COLS["cpus"], default="1")
    mem = pick(row, CANDIDATE_COLS["mem"], default="1Gi")
    dur = pick(row, CANDIDATE_COLS["duration"], default="60")

    cpu_str = str(int(float(cpus))) if str(cpus).replace(".", "", 1).isdigit() else "1"
    mem_str = to_gi(mem)
    dur_str = str(int(float(dur)))

    name = f"job-{jid}".lower().replace("_", "-")
    cmd = f"echo start; yes > /dev/null & PID=$!; sleep {dur_str}; kill $PID; echo done"

    task_container = client.V1Container(
        name="task",
        image=IMAGE,
        command=["/bin/bash", "-lc", cmd],
        resources=client.V1ResourceRequirements(
            requests={"cpu": cpu_str, "memory": mem_str},
            limits={"cpu": cpu_str, "memory": mem_str},
        ),
    )

    containers = [task_container]
    affinity = None

    if FORCE_SITE:
        affinity = client.V1Affinity(
            node_affinity=client.V1NodeAffinity(
                required_during_scheduling_ignored_during_execution=client.V1NodeSelector(
                    node_selector_terms=[
                        client.V1NodeSelectorTerm(
                            match_expressions=[
                                client.V1NodeSelectorRequirement(
                                    key="site",
                                    operator="In",
                                    values=[FORCE_SITE],
                                )
                            ]
                        )
                    ]
                )
            )
        )

    resource_class = row.get("resource_class", "").strip().lower()
    gpu_count = 0
    try:
        gpu_count = int(float(row.get("gpu_count", "0") or 0))
    except Exception:
        gpu_count = 0
    if gpu_count > 0:
        gpu_value = str(max(gpu_count, 1))
        for res_list in (task_container.resources.requests, task_container.resources.limits):
            res_list["nvidia.com/gpu"] = gpu_value

    if ENABLE_PROM:
        metrics_container = client.V1Container(
            name="metrics-agent",
            image=PROM_IMAGE,
            image_pull_policy="IfNotPresent",
            env=[
                client.V1EnvVar(name="METRICS_PORT", value=str(PROM_PORT)),
                client.V1EnvVar(name="TARGET_CONTAINER", value="task"),
            ],
            ports=[client.V1ContainerPort(container_port=PROM_PORT)],
            resources=client.V1ResourceRequirements(
                requests={"cpu": PROM_CPU_REQ, "memory": PROM_MEM_REQ},
                limits={"cpu": PROM_CPU_LIMIT, "memory": PROM_MEM_LIMIT},
            ),
        )
        containers.append(metrics_container)

    node_selector = None
    if resource_class == "gpu" or gpu_count > 0:
        node_selector = {"gpu": "true"}

    podspec_kwargs = dict(restart_policy="Never", containers=containers, affinity=affinity)
    if node_selector:
        podspec_kwargs["node_selector"] = node_selector
    if TARGET_SCHEDULER_NAME:
        podspec_kwargs["scheduler_name"] = TARGET_SCHEDULER_NAME
    if ENABLE_PROM:
        podspec_kwargs["share_process_namespace"] = True
    podspec = client.V1PodSpec(**podspec_kwargs)

    annotations = {"ciw/cpus": cpu_str, "ciw/duration_req_s": dur_str}
    if DEFAULT_MAX_DEFER_FRAC > 0:
        try:
            annotations["ciw/max_defer_s"] = str(int(float(dur_str) * DEFAULT_MAX_DEFER_FRAC))
        except Exception:
            pass
    preferred_site = (row.get("preferred_site") or "").strip()
    if preferred_site:
        annotations["ciw/preferred_site"] = preferred_site

    if ENABLE_PROM:
        annotations.update(
            {
                "prometheus.io/scrape": "true",
                "prometheus.io/path": PROM_PATH,
                "prometheus.io/port": str(PROM_PORT),
                "prometheus.io/scrape-interval": PROM_INTERVAL,
            }
        )

    labels = {
        "ciw/scheduler": SCHEDULER_LABEL,
        "ciw/workload_id": jid,
        "ciw/eligible": "true",
    }
    if resource_class:
        labels["ciw/resource_class"] = resource_class
    
    template = client.V1PodTemplateSpec(
        metadata=client.V1ObjectMeta(labels=labels, annotations=annotations),
        spec=podspec,
    )
    spec = client.V1JobSpec(template=template)

    return (
        client.V1Job(
            api_version="batch/v1",
            kind="Job",
            metadata=client.V1ObjectMeta(name=name),
            spec=spec,
        ),
        jid,
    )


def main():
    try:
        config.load_incluster_config()
    except Exception:
        config.load_kube_config()

    api = client.BatchV1Api()
    csv_path = os.getenv("CSV_PATH", "/data/workloads.csv")

    with open(csv_path, newline="") as handle:
        reader = csv.DictReader(handle)
        count = 0
        prev_submit = None
        for row in reader:
            if SKIP_GPU_JOBS:
                rc = (row.get("resource_class") or "").strip().lower()
                gpu_count = parse_float(row.get("gpu_count", "0"), 0.0)
                if rc == "gpu" or gpu_count > 0:
                    continue
            if count >= MAX_JOBS:
                break
            submit_at = parse_ts(row.get("submit"))
            if prev_submit is not None and submit_at is not None:
                delta = (submit_at - prev_submit).total_seconds()
                if delta > 0:
                    time.sleep(delta)
            elif submit_at is None and prev_submit is not None:
                time.sleep(SLEEP_BETWEEN)
            job, jid = job_from_row(row, index=count)
            api.create_namespaced_job(namespace=NAMESPACE, body=job)
            print(f"submitted {jid}")
            count += 1
            if submit_at is not None:
                prev_submit = submit_at
            else:
                prev_submit = None
            if submit_at is None and prev_submit is None:
                time.sleep(SLEEP_BETWEEN)


if __name__ == "__main__":
    main()
