# Erste Schritte

Dieses Repository enthält den `service-account-operator`.

## Voraussetzungen

- Die `ServiceAccountRequest`- und `ServiceAccountProducer`-CRDs sind im Cluster installiert

Falls die CRDs noch nicht installiert sind, können sie zum Beispiel als Component installiert werden:

```shell
kubectl apply -f - <<EOF
apiVersion: k8s.cloudogu.com/v1
kind: Component
metadata:
  name: k8s-serviceaccount-crd
  labels:
    app: ces
    app.kubernetes.io/name: k8s-serviceaccount-crd
spec:
  name: k8s-serviceaccount-crd
  namespace: k8s
  version: 1.0.0
EOF
```

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
