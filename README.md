# Gateman

[![CI](https://github.com/ksgate/ksgate/actions/workflows/ci.yml/badge.svg)](https://github.com/ksgate/ksgate/actions/workflows/ci.yml)
[![Docker](https://img.shields.io/github/v/tag/ksgate/ksgate?label=Docker)](https://github.com/ksgate/ksgate/pkgs/container/ksgate)
[![Helm](https://img.shields.io/github/v/tag/ksgate/ksgate?label=Helm)](https://github.com/ksgate/ksgate/pkgs/container/ksgate%2Fksgate)

Gateman is a Kubernetes controller that manages the scheduling of pods using declarative gates and conditions.

## Description
Avoid wasting resources scheduling pods that are not ready to run due to unsatisfied dependencies.

With Gateman you can easily declare your dependencies using a [PodSchedulingGate](https://kubernetes.io/docs/reference/kubernetes-api/workload-resources/pod-scheduling-gate-v1/) plus an annotation that describes a condition on a resource. Then, let Gateman take care of the rest.

Here's a quick example:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: my-app
  annotations:
    # Ready deployment
    k8s.ksgate.org/database-deployment: |
      {
        "apiVersion": "apps/v1",
        "kind": "Deployment",
        "name": "database",
        "expression": "resource.status.updatedReplicas >= 1"
      }
    # Existing service
    k8s.ksgate.org/database-service: |
      {
        "apiVersion": "v1",
        "kind": "Service",
        "name": "database"
      }
spec:
  containers:
  - name: my-app
    image: my-app
    env:
    - name: DATABASE_HOST
      value: database
  schedulingGates:
  - name: k8s.ksgate.org/database-deployment
  - name: k8s.ksgate.org/database-service
```

This pod will not be scheduled until the `database` deployment has at least 1 updated replica and the `database` service is present.

### Why?

Kubernetes does not have a built-in dependency/ordering mechanism for pods that need to be scheduled in a specific order or based on the availability of a resource.

One common workaround is to use init containers that wait for the necessary depedencies to be ready. Another is simply to keep trying (and failing) until the dependency is ready. None of these approaches are ideal as they add overhead to the pod's lifecycle, waste resources or suffer from limitations with respect to probe thresholds and timeouts which require manual recovery and complex (and delicate) tuning.

Luckily, with the arrival of Kubernetes 1.30 and [Pod Scheduling Readiness](https://kubernetes.io/docs/concepts/scheduling-eviction/pod-scheduling-readiness/) the better way to achieve this is to gate the scheduling of a pod using a [PodSchedulingGate](https://kubernetes.io/docs/reference/kubernetes-api/workload-resources/pod-scheduling-gate-v1/).

Furthermore, gated scheduling is essentially resource free with __no__ time limits. Resources may safely remain gated indefinitely. Gates are also meant to be descriptive enough to make it clear why a pod is not scheduled when used.

However, this approach requires an external actor to understand the meaning of the gate and to remove the gate once the pod can be scheduled.

This is where Gateman comes in. It automates the process of evaluating conditions and removing gates.

## Documentation

### Gates
Gates must be prefixed with `k8s.ksgate.org/` to be recognized by Gateman. The suffix is used to identify individual gates and is arbitrary.

### Annotations
An annotation by the same name is used to hold the condition which is JSON Object that conforms to the [condition schema](condition.schema.json).

### Expressions
Expressions (when present) are [CEL expressions](#cel) that must evaluate to `true`. The absence of an expression is interpreted as a simple existence check on the resource.

### Variables
The CEL environment contains two variables: `resource` and `pod`. `resource` gives access to the resource being evaluated and `pod` gives access to the pod that is being scheduled. Any complex logic over these variables can be expressed using CEL. `resource` and `pod` are the complete JSON representation of the resource and pod respectively.

#### CEL (Common Expression Language)
CEL expressions are a powerful feature that allow for complex filtering and validation. For more information, see the [CEL documentation](https://kubernetes.io/docs/reference/using-api/cel/).

## Installation

The Gateman controller image is available at [ghcr.io/ksgate/ksgate](https://github.com/ksgate/ksgate/pkgs/container/ksgate).

However, I recommend using the Helm chart to install Gateman.

### Helm Chart

The Helm chart is available as an OCI artifact at [oci://ghcr.io/ksgate/ksgate/ksgate](oci://ghcr.io/ksgate/ksgate/ksgate).

I recommend placing the chart resources in a dedicated namespace since this is generally used as a cluster-wide controller.

To install the chart, run:

```sh
helm install ksgate oci://ghcr.io/ksgate/ksgate/ksgate --version <version> --namespace ksgate-system --create-namespace
```

## License

Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

