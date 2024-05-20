#!/bin/bash

set -xe
echo
echo "Applying test resources."
echo
kubectl apply -f e2e-pg16-gen-db-same-namespace.yaml
# List the database in the namespace
kubectl get -o yaml --namespace e2e-pg16 database.pgdb.storage.meschbach.com/database-sample

echo
echo "Waiting for target secret to be created."
echo
set +e
kubectl wait --namespace e2e-pg16 --for='jsonpath={.status.ready}=true' database.pgdb.storage.meschbach.com/database-sample
if [ -z $? ]; then
  echo "Database ready."
  kubectl get -o yaml --namespace e2e-pg16 database.pgdb.storage.meschbach.com/database-sample
else
  set +x
  echo "========================================"
  echo "Timed wait on database readiness failed"
  echo "========================================"
  echo "Database CRD"
  echo "--------------------"
  kubectl get -o yaml --namespace e2e-pg16 database.pgdb.storage.meschbach.com/database-sample
  echo "--------------------"
  echo "Postgres service"
  echo "--------------------"
  kubectl --namespace e2e-pg16 get svc pg16 -o yaml
  echo "--------------------"
  echo "Postgres endpoints"
  echo "--------------------"
  kubectl --namespace e2e-pg16 get endpointslices -o yaml
  echo "--------------------"
  echo "Controller Log"
  echo "--------------------"
  kubectl logs --namespace pgdb-system --selector control-plane=controller-manager --lines -1
  exit -1
fi
set -e

echo
echo "Waiting new secret to be populated, then verify"
echo
kubectl wait --namespace e2e-pg16 --for='jsonpath={.data.host}="cGcxNi5lMmUtcGcxNi5zdmMuY2x1c3Rlci5sb2NhbA=="' secret/database-sample
# ensure the target host makes sense
db_name=$(kubectl get secret --namespace e2e-pg16 -o json database-sample |jq -r '.data.database' |base64 -d)
if [ "$db_name" = "postgres" ]; then
    echo "Target database may not be postgres."
fi

# TODO: need to trap signals to cleanup locally in bad test cases.
echo
echo "Destroy the resource"
echo
kubectl delete -f e2e-pg16-gen-db-same-namespace.yaml

echo
echo "Verify the secret has been destroy"
echo
while true ; do
  echo "Working..."
  result=$(kubectl --namespace e2e-pg16 get secrets |grep database-sample) # -n shows line number
  if [ -z "$result" ] ; then
    echo "verified secret destroyed"
    break
  fi
  sleep 2
done
