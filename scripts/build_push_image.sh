#!/usr/bin/env bash
set -euo pipefail

# Repo root
SCRIPT_DIR="$( cd -- "$( dirname -- "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )"
cd "$SCRIPT_DIR"/..

# Registry & tags
REG="${REG:-docker.io/goncaloferreirauva}"
TAG="${TAG:-0.1}"

echo "Using registry: $REG, tag: $TAG"

# Build controller
echo "Building ci-aware-controller..."
docker build ./controller -f ./controller/Dockerfile -t "$REG/ci-aware-controller:$TAG"
docker tag "$REG/ci-aware-controller:$TAG" "$REG/ci-aware-controller:latest"
docker push "$REG/ci-aware-controller:$TAG"
docker push "$REG/ci-aware-controller:latest"

# Build workload replayer
echo "Building workload-replayer..."
docker build ./workloads -f ./workloads/Dockerfile -t "$REG/workload-replayer:$TAG"
docker tag "$REG/workload-replayer:$TAG" "$REG/workload-replayer:latest"
docker push "$REG/workload-replayer:$TAG"
docker push "$REG/workload-replayer:latest"

# (Optional) Forecast service stub
if [ -f "./forecast_service/Dockerfile" ]; then
  echo "Building forecastservice..."
  docker build ./forecast_service -f ./forecast_service/Dockerfile -t "$REG/forecastservice:$TAG"
  docker tag "$REG/forecastservice:$TAG" "$REG/forecastservice:latest"
  docker push "$REG/forecastservice:$TAG"
  docker push "$REG/forecastservice:latest"
fi

echo "All images built and pushed."
