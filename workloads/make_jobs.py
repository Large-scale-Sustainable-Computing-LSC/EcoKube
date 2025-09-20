import csv, os, time, base64, json
from kubernetes import client, config

NAMESPACE = os.getenv("WORKLOAD_NS","workloads")

def job_spec(row):
    name = f"job-{row['job_id']}"
    cpus = str(row.get('cpus', '1'))
    mem  = str(row.get('mem_gb','1')) + "Gi"
    dur  = row.get('duration_s','60')
    img  = row.get('image','ubuntu:22.04')

    return client.V1Job(
      api_version="batch/v1", kind="Job",
      metadata=client.V1ObjectMeta(name=name, labels={
        "ciw/scheduler": "baseline",
        "ciw/workload_id": row['job_id']
      }, annotations={
        "ciw/cpus": cpus, "ciw/duration_req_s": str(dur)
      }),
      spec=client.V1JobSpec(template=client.V1PodTemplateSpec(
        spec=client.V1PodSpec(
          restart_policy="Never",
          containers=[client.V1Container(
            name="task", image=img,
            command=["/bin/bash","-lc",
                     f"echo start; yes > /dev/null & PID=$!; sleep {dur}; kill $PID; echo done"],
            resources=client.V1ResourceRequirements(
              requests={"cpu": cpus, "memory": mem},
              limits={"cpu": cpus, "memory": mem}
            )
          )]
        )
      )))
    )

def main():
    # in-cluster if possible
    try: config.load_incluster_config()
    except: config.load_kube_config()
    api = client.BatchV1Api()
    with open("/data/workloads.csv") as f:
        r = csv.DictReader(f)
        for row in r:
            jb = job_spec(row)
            api.create_namespaced_job(namespace=NAMESPACE, body=jb)
            print("submitted", row['job_id'])
            time.sleep(1)

if __name__ == "__main__":
    main()
