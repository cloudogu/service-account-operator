# Allgemeine Informationen über Service-Accounting

Service Accounts nach Dogu-API v3 beschreiben einen Mechanismus, der übergreifend über Dogus und Komponenten
technische Zugänge ermöglicht. Hierbei fungiert der Service-Account-Operator als strikter Vermittler zwischen Producer
und Consumer. 

Ein **Producer** stellt eine Service-Account-Producer (SAPR) _Custom Resource_ zur Verfügung, die seine eigene API
zur Erzeugung von Zugängen beschreibt. Da jede Software unterschiedliche Zugangsnotwendigkeiten besitzt,
beschreibt sie auch, wie die Zugangsdaten inhaltlich aufgebaut sind. Z. B. könnte ein Producer lediglich Kontoname und
Passphrase zurückliefern, während ein anderer zusätzlich Schema- oder URL-Informationen für die Adressierung des Zugangs
benötigt und auch diese bereitstellt.

Producer müssen hierbei eine [Service-Account-Producer-API](openapi.yaml) implementieren und im jeweiligen Softwareteil
(Dogu oder Komponente) zur Laufzeit anbieten. Diese API darf aus Sicherheitsgründen ausschließlich vom
Service-Account-Operator verwendet werden.

**Consumer** hingegen beschreiben ihren Wunsch nach einem Service Account bei einem bestimmten Producer
und in welchem Secret dieser Service Account vom Service-Account-Operator abgelegt werden soll. 

![Ein DSA-Producer (unbestimmt, ob Dogu oder Component) enthält einn SAPR-Ressource mit dem Producernamen "gareth".
Dem gegenüber stehen ein DSA-Consumer-Dogu und eine DSA-Consumer-Komponente, die ihrerseits einen DSA mit dem
Producernamen "gareth" erfragen. Der Service-Account-Operator erkennt die Übereinstimmung und erzeugt jeweils ein Secret
welches dem DSA zwischen Consumer und Producer entspricht.](images/relationship_sare_sapr.drawio.png
"DSA-Beziehung zwischen unterschiedlichen DSA-Consumern und einem DSA-Producer")

## Beispiel-Ressourcen

Das [K8s-Samples-Repo](https://github.com/cloudogu/k8s-ecosystem-samples/tree/main/serviceaccount) enthält Beispiele,
wie SAPR-CRs (für Producer) und SARE-CRs (für Consumer) aussehen können.
Dabei muss sich ein Consumer an die Struktur des Producers halten.