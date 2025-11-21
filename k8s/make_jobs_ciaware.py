import csv, os, time
from kubernetes import client, config

NAMESPACE = os.getenv("WORKLOAD_NS", "workloads")
IMAGE = os.getenv("JOB_IMAGE", "ubuntu:22.04")
SLEEP_BETWEEN = float(os.getenv("SUBMIT_EVERY_SEC", "0.5"))
SCHEDULER_NAME = os.getenv("SCHEDULER_NAME", "ci-aware")

CANDIDATE_COLS = {
    "id": ["job_id", "jobid", "task_id", "taskid", "id"],
    "cpus": ["cpus", "cpu", "num_cpu", "requested_cpu_cores", "cpu_request", "cpu_cores"],
    "mem": ["mem_gb", "memory_gb", "mem", "memory", "requested_memory_bytes", "memory_request", "ram_gb"],
    "duration": ["duration_s", "runtime_s", "run_time", "runtime_sec", "duration", "walltime_s", "wall_time_sec"],
}
MAX_JOBS = int(os.getenv("MAX_JOBS", "50"))


def pick(row, keys, default=None):
    for k in keys:
        if k in row and row[k] not in ("", None):
            return row[k]
    return default


def parse_float(x, default=0.0):
    try:
        return float(x)
    except Exception:
        return default


def to_gi(mem_val):
    s = str(mem_val).strip().lower()
    if s.endswith("gi"):
        return s
    if s.endswith("gb") or s.endswith("g"):
        v = parse_float(s.rstrip("gb").rstrip("g"), 1.0)
        return f"{max(v, 0.25):g}Gi"
    try:
        b = float(s)
        gi = max(b / (1024**3), 0.25)
        return f"{gi:g}Gi"
    except Exception:
        v = parse_float(s, 1.0)
        return f"{max(v, 0.25):g}Gi"


def job_from_row(row, index=None):
    jid = str(index).zfill(3) if index is not None else str(
        pick(row, CANDIDATE_COLS["id"], default=f"anon-{int(time.time()*1000)}")
    )
    cpus = pick(row, CANDIDATE_COLS["cpus"], default="1")
    mem = pick(row, CANDIDATE_COLS["mem"], default="1Gi")
    dur = pick(row, CANDIDATE_COLS["duration"], default="60")

    cpus = str(int(float(cpus))) if str(cpus).replace(".", "", 1).isdigit() else "1"
    mem = to_gi(mem)
    dur = str(int(float(dur)))

    name = f"job-{jid}".lower().replace("_", "-")
    cmd = f"echo start; yes > /dev/null & PID=$!; sleep {dur}; kill $PID; echo done"

    container = client.V1Container(
        name="task",
        image=IMAGE,
        command=["/bin/bash", "-lc", cmd],
        resources=client.V1ResourceRequirements(
            requests={"cpu": cpus, "memory": mem},
            limits={"cpu": cpus, "memory": mem},
        ),
    )

    affinity = client.V1Affinity(
        node_affinity=client.V1NodeAffinity(
            required_during_scheduling_ignored_during_execution=client.V1NodeSelector(
                node_selector_terms=[
                    client.V1NodeSelectorTerm(
                        match_expressions=[
                            client.V1NodeSelectorRequirement(
                                key="site", operator="In", values=["B"]
                            )
                        ]
                    )
                ]
            )
        )
    )

    podspec = client.V1PodSpec(
        restart_policy="Never",
        containers=[container],
        affinity=affinity,
        scheduler_name=SCHEDULER_NAME,
    )

    template = client.V1PodTemplateSpec(
        metadata=client.V1ObjectMeta(
            labels={
                "ciw/scheduler": SCHEDULER_NAME,
                "ciw/workload_id": jid,
                "ciw/eligible": "true",
            },
            annotations={"ciw/cpus": cpus, "ciw/duration_req_s": dur},
        ),
        spec=podspec,
    )
    spec = client.V1JobSpec(template=template)
    return client.V1Job(
        api_version="batch/v1",
        kind="Job",
        metadata=client.V1ObjectMeta(name=name),
        spec=spec,
    ), jid


def main():
    try:
        config.load_incluster_config()
    except Exception:
        config.load_kube_config()
    api = client.BatchV1Api()

    csv_path = os.getenv("CSV_PATH", "/data/workloads.csv")
    with open(csv_path, newline="") as f:
        r = csv.DictReader(f)
        count = 0
        for row in r:
            if count >= MAX_JOBS:
                break
            job, jid = job_from_row(row, index=count)
            api.create_namespaced_job(namespace=NAMESPACE, body=job)
            print(f"submitted {jid}")
            count += 1
            time.sleep(SLEEP_BETWEEN)


if __name__ == "__main__":
    main()
