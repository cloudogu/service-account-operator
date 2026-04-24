# Erste Schritte

Dieses Repository enthält den `service-account-operator`.

## Voraussetzungen

- Die `ServiceAccountRequest`- und `ServiceAccountProducer`-CRDs sind im Cluster installiert

Prüfen, ob die CRDs vorhanden sind:

```shell
kubectl get crd serviceaccountrequests.k8s.cloudogu.com
kubectl get crd serviceaccountproducers.k8s.cloudogu.com
```

## Operator als Component installieren

Aus diesem Repository:

```shell
make component-apply
```

Dieses Target baut und paketiert das Helm-Chart und wendet die `Component`-Ressource an.

## Installation verifizieren

```shell
kubectl -n ecosystem get deployment service-account-operator
kubectl -n ecosystem get pods -l app.kubernetes.io/name=service-account-operator
```
