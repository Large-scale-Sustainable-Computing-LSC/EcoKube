#!/bin/bash
# Depending on where we're executing this script, we need it's "absolute" path.
SCRIPT_DIR=$( cd -- "$( dirname -- "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )
cd $SCRIPT_DIR/../helm/

kubectl apply -f ../helm/manual_config/external-pv.yaml
kubectl apply -f ../helm/manual_config/podmonitor-compute.yaml
kubectl apply -f ../helm/manual_config/scaph-nginx-configmap.yaml
# kubectl apply -f ../helm/manual_config/powertrace-configmap.yaml

helm install eu-cluster . -n eu-central --create-namespace
helm install eu-monitoring prometheus-community/kube-prometheus-stack \
  --set-file powertraceCsv=../helm/data/powertrace.csv \
  --set-file powertraceKey=../helm/data/key.json \
  -n eu-central