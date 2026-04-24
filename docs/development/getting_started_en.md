# Getting Started

This repository contains the `service-account-operator`.

## Prerequisites

- The `ServiceAccountRequest` and `ServiceAccountProducer` CRDs installed in the cluster

Check if the CRDs exist:

```shell
kubectl get crd serviceaccountrequests.k8s.cloudogu.com
kubectl get crd serviceaccountproducers.k8s.cloudogu.com
```

## Install the Operator as a Component

From this repository:

```shell
make component-apply
```

This target builds and packages the Helm chart and applies the `Component` resource.

## Verify Installation

```shell
kubectl -n ecosystem get deployment service-account-operator
kubectl -n ecosystem get pods -l app.kubernetes.io/name=service-account-operator
```
