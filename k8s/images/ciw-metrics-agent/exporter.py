import os
import time

import psutil
from prometheus_client import Gauge, start_http_server

PORT = int(os.getenv("METRICS_PORT", "9101"))
INTERVAL = float(os.getenv("SCRAPE_INTERVAL", "2"))
TARGET = os.getenv("TARGET_CONTAINER", "task")

CPU_SECONDS = Gauge("ciw_pod_cpu_seconds_total", "Total CPU seconds consumed by the pod", ["container"])
MEM_RSS = Gauge("ciw_pod_memory_rss_bytes", "Resident set size bytes for pod processes", ["container"])
PROC_COUNT = Gauge("ciw_pod_process_count", "Number of tracked processes", ["container"])
START_TS = Gauge("ciw_pod_start_timestamp_seconds", "Exporter start timestamp", ["container"])

START_TS.labels(TARGET).set(time.time())


def collect_metrics():
    me = os.getpid()
    total_cpu = 0.0
    total_rss = 0
    proc_count = 0
    for proc in psutil.process_iter(["pid", "cpu_times", "memory_info"]):
        try:
            if proc.pid == me or proc.ppid() == me:
                continue
            cpu_times = proc.cpu_times()
            total_cpu += getattr(cpu_times, "user", 0.0) + getattr(cpu_times, "system", 0.0)
            mem = proc.memory_info()
            total_rss += getattr(mem, "rss", 0)
            proc_count += 1
        except (psutil.NoSuchProcess, psutil.AccessDenied):
            continue
    return total_cpu, total_rss, proc_count


def main():
    start_http_server(PORT)
    while True:
        cpu, rss, count = collect_metrics()
        CPU_SECONDS.labels(TARGET).set(cpu)
        MEM_RSS.labels(TARGET).set(rss)
        PROC_COUNT.labels(TARGET).set(count)
        time.sleep(max(INTERVAL, 0.5))


if __name__ == "__main__":
    main()
