#!/bin/bash

ns=${1:-e2e-pg16}
set -e
cd e2e
kubectl create namespace $ns
kubectl apply --namespace $ns -f pg16.yaml

echo
echo Wait on deployment to be ready
echo
kubectl wait --namespace $ns --timeout=60s --for=condition=available deployment e2e-pg16

echo
echo "Database reports ready"
echo
echo "Endpoints"
kubectl --namespace $ns get endpointslices -o yaml
