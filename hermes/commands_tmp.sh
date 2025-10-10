# This is a set of commands for development mode, because I (@goncalo) am lazy. :)

# kubectl rollout restart statefulset compute -n eu-central
# kubectl rollout restart deployment centralunit -n eu-central


# kubectl exec -it centralunit-864794d4d-gl4nm -n eu-central -- curl http://compute-0.compute:9999/forward-metrics
# curl http://compute-0.compute:9999/forward-metrics
# kubectl logs pod/compute-0 -n eu-central


# ./scripts/remove_helm.sh
# ./scripts/start_helm.sh

# METRICS_FILENAME="metrics_2025-07-05_20-13-07.csv" \
# kubectl cp eu-central/centralunit-864794d4d-gl4nm:/data/$METRICS_FILENAME ./csv_exports/$METRICS_FILENAME

# kubectl cp eu-central/centralunit-864794d4d-gl4nm:/data/metrics_aggregated.csv csv_exports/metrics_aggregated.csv
# scp goncalo@mc-a4.lab.uvalight.net:~/KubeEnergyScheduler/csv_exports/metrics_aggregated.csv ~/Desktop/metrics_aggregated.csv

# kubectl exec -it centralunit-864794d4d-gl4nm -n eu-central -- sh

# kubectl exec -it centralunit-864794d4d-gl4nm -n eu-central -- curl http://compute-0.compute:9999/forward-metrics
# kubectl cp eu-central/centralunit-864794d4d-gl4nm:/data/metrics_aggregated.csv csv_exports/metrics_aggregated.csv
# scp goncalo@mc-a4.lab.uvalight.net:~/KubeEnergyScheduler/csv_exports/metrics_aggregated.csv ~/Desktop/metrics_aggregated.csv

# kubectl cp eu-central/centralunit-864794d4d-gl4nm:/data/container_metrics.csv csv_exports/container_metrics.csv

# helm dependency update helm/
# kubectl port-forward svc/eu-cluster-prometheus-server 9090:80 -n eu-central
# kubectl rollout restart deployment eu-cluster-prometheus-server -n eu-central

# Test if Prometheus can connect to scaphandre on compute-0
# kubectl exec -n eu-central -it eu-cluster-prometheus-server-69b8d46f49-hxdsg -- curl compute-0.eu-central.svc.cluster.local:8080/metrics


# Execute range metrics' export inside of the cluster
# kubectl exec -n eu-central -it deploy/centralunit -- sh
# curl http://centralunit.eu-central.svc.cluster.local:8080/metrics-export-range

# kubectl exec -it -n eu-central deploy/centralunit -- curl http://localhost:8080/metrics-export-range
# kubectl cp eu-central/centralunit-864794d4d-gl4nm:/data/full_scaphandre_metrics.csv csv_exports/full_scaphandre_metrics.csv

# kubectl exec -it powertrace-798b59c4dd-h9cft -n eu-central -- curl -X POST http://localhost:5000/send-workloads
