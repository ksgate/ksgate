## Simple example

Deploy the Gateman controller first.

```sh
helm install gateman oci://ghcr.io/ksgate/ksgate/ksgate --namespace gateman-system --create-namespace
```

Deploy the pod that needs the configmap found in `01_pod_needs_configmap.yaml`.

```shell
kubectl apply -f samples/pod_depends_on_configmap/01_pod_needs_configmap.yaml
```

The pod will be gated from being scheduled.

Deploy the configmap found in `02_configmap_needed.yaml`.

```shell
kubectl apply -f samples/pod_depends_on_configmap/02_configmap_needed.yaml
```

The pod will be scheduled.
