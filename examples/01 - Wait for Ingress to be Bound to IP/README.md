# Example #01 - Wait for Ingress to be Bound to an IP

Deploy the KSGate controller first.

Deploy the remaining resources in order. After each one notice the state of the pods.

```shell
kubectl apply -f 01_nginx_pod.yaml
kubectl apply -f 02_configmap.yaml
kubectl apply -f 03_service.yaml
kubectl apply -f 04_ingress.yaml
```