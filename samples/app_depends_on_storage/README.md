## Simple example

Deploy the KSGate controller first.

```sh
helm install ksgate oci://ghcr.io/ksgate/charts/ksgate --namespace ksgate-system --create-namespace
```

Deploy the remaining resources in order. After each one notice the state of the pods.

```sh
kubectl apply -f samples/app_depends_on_storage/01_app_needs_storage.yaml
kubectl apply -f samples/app_depends_on_storage/02_postgres_deployment.yaml
kubectl apply -f samples/app_depends_on_storage/03_postgres_service.yaml
kubectl apply -f samples/app_depends_on_storage/04_postgres_configmap.yaml
kubectl apply -f samples/app_depends_on_storage/05_postgres_pv.yaml
```