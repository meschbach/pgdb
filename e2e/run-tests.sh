#!/bin/bash

set -xe
kubectl apply -f e2e-pg16-gen-db-same-namespace.yaml

echo
echo "Waiting for target secret to be created."
echo
until kubectl get secrets --namespace e2e-pg16 |grep database-sample; do sleep 5; done

echo
echo "Waiting new secret to be populated, then verify"
echo
kubectl wait --namespace e2e-pg16 --for='jsonpath={.data.host}="cGcxNi5lMmUtcGcxNi5zdmMuY2x1c3Rlci5sb2NhbA=="' secret/database-sample
# ensure it is a new database
db_name=$(kubectl get secret --namespace e2e-pg16 -o json database-sample |jq -r '.data.database' |base64 -d)
if [ "$db_name" = "postgres" ]; then
    echo "New database was not created."
fi

kubectl delete -f e2e-pg16-gen-db-same-namespace.yaml
