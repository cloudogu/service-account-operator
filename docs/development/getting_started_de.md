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

## Testen von SARE/SAPR

Für Tests der Funktionalitäten bietet sich die SAPR-ready Komponente [k8s-prometheus](https://github.com/cloudogu/k8s-prometheus) an

Ein Service-Account kann dann mittels SARE aus dem [K8s-Sample-Repo](https://github.com/cloudogu/k8s-ecosystem-samples/tree/main/serviceaccount) erstellt werden:

1. k8s-prometheus-Stand mit SAPR auf Cluster anwenden
   - z. B. mittels `make component-apply`
2. SARE für Prometheus auf Cluster anwenden
   - `kubectl -n ecosystem apply -f https://raw.githubusercontent.com/cloudogu/k8s-ecosystem-samples/refs/heads/main/serviceaccount/requester.yaml`
3. ein Secret `grafana-prometheus-credentials` wurde im Cluster vom SA-Operator angelegt