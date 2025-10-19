#!/bin/bash
# Depending on where we're executing this script, we need it's "absolute" path.
SCRIPT_DIR=$( cd -- "$( dirname -- "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )
cd $SCRIPT_DIR/../helm/
# Restart Helm release with `eu-cluster`.
helm upgrade --install eu-cluster . -n eu-central --create-namespace
kubectl rollout restart statefulset compute -n eu-central
kubectl rollout restart deployment centralunit -n eu-central