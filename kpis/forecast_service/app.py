import json, os
from fastapi import FastAPI, Response
from prometheus_client import CollectorRegistry, Gauge, generate_latest, CONTENT_TYPE_LATEST

SITES_PATH = os.getenv("SITES_PATH","/etc/ci-aware/sites.json")
app = FastAPI()

@app.get("/metrics")
def metrics():
    with open(SITES_PATH) as f:
        sites = json.load(f)
    reg = CollectorRegistry()
    g = Gauge("ci_current_g_per_kwh","Static CI nowcast", ["site"], registry=reg)
    for sid, v in sites.items():
        ci = float(v.get("ci", 400.0))
        g.labels(site=sid).set(ci)
    return Response(generate_latest(reg), media_type=CONTENT_TYPE_LATEST)
