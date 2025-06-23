# Example #02 - Wait for Dependent Services to be Ready Without Polling

Deploy the KSGate controller first.

Deploy the remaining resources in order. After each one notice the state of the pods.

```sh
kubectl apply -f 01_app_deployment.yaml
kubectl apply -f 02_postgres_deployment.yaml
kubectl apply -f 03_postgres_service.yaml
kubectl apply -f 04_postgres_configmap.yaml
kubectl apply -f 05_postgres_pv.yaml
```