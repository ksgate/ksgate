## Example #01

Deploy the KSGate controller first.

Deploy the remaining resources in order. After each one notice the state of the pods.

```shell
kubectl apply -f examples/01/01_nginx_pod.yaml
kubectl apply -f examples/01/02_configmap.yaml
```