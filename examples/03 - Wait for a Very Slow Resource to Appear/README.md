## Example #03 - Wait for a Very Slow Resource to Appear

Deploy the KSGate controller first.

We are going to simulate by waiting until 2 minutes after the Config Map is created.

Deploy the remaining resources in order. After each one notice the state of the pods.

```shell
kubectl apply -f 01_nginx_pod.yaml
kubectl apply -f 02_configmap.yaml
kubectl apply -f 03_service.yaml
kubectl apply -f 04_ingress.yaml
```