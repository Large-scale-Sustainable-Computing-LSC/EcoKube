#!/bin/bash

set -a 
source .env
set +a
if [ -z "$GHCR_TOKEN" ]; then
  echo "Error: GHCR_TOKEN is not set in .env/GHCR_TOKEN"
  exit 1
fi
echo $GHCR_TOKEN | docker login ghcr.io -u g-uva --password-stdin

kubectl create secret docker-registry regcred \
  --docker-username=goncaloferreirauva \
  --docker-password=$GHCR_TOKEN \
  --docker-email=goncalo.ferreira@student.uva.nl \
  -n eu-central