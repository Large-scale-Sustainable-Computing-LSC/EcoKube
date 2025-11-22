#!/usr/bin/env bash
set -euo pipefail

# Repo root
SCRIPT_DIR="$( cd -- "$( dirname -- "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
cd "$REPO_ROOT"

# Registry & tags
# REG="${REG:-goncaloferreirauva}"
# TAG="${TAG:-0.1}"

export DOCKER_BUILDKIT=1

echo "Using the docker account: goncaloferreirauva, with 'latest' tag."

# Build controller
echo "Building ci-aware-controller..."
docker build "$REPO_ROOT" -f "$REPO_ROOT/kubenergysched/controller/Dockerfile" -t "goncaloferreirauva/ci-aware-controller:latest"
docker push "goncaloferreirauva/ci-aware-controller:latest"

# Build workload replayer
echo "Building workload-replayer..."
docker build "$REPO_ROOT" -f "$REPO_ROOT/kubenergysched/workloads/Dockerfile" -t "goncaloferreirauva/workload-replayer:latest"
docker push "goncaloferreirauva/workload-replayer:latest"

# (Optional) Forecast service stub
if [ -f "./forecast_service/Dockerfile" ]; then
  echo "Building forecastservice..."
  docker build "./forecast_service" -f "./forecast_service/Dockerfile" -t "goncaloferreirauva/forecastservice:latest"
  docker push "goncaloferreirauva/forecastservice:latest"
fi

echo "All images built and pushed."
