# Operative Szenarien verstehen

Dogu Service Accounts nach der Dogu-API v3 verfolgen im Cloudogu EcoSystem (CES) ein ähnliches Ziel wie die Service
Accounts der Dogu-API v2, hier jedoch erweitert und mit leicht anderer Ausrichtung. In Dogu-API v2 waren auch
Hilfscontainer wie Datenbanken Dogus, die häufig für die Datenhaltung einen Service Account zur Sicherung und
Dogu-Trennung notwendig machten.

Mit der Dogu-API v3 ist dies für Hilfscontainer nicht mehr nötig, da Dogu Helm-Charts eigene, weitere beliebige
Container hervorbringen können, für deren Zugriff kein CES-übergreifender Mechanismus nötig ist. Allerdings ist es
weiterhin möglich, das Dogus miteinander kommunizieren können. Hierzu werden weiterhin DSA benötigt. Darüber hinaus
gelten DSA nicht nur für Dogus, sondern auch für CES-Komponenten. Beide können sowohl als DSA Consumer und/oder als DSA
Producer auftreten.

Dieses Dokument beschreibt Szenarien, in denen DSAs erzeugt, modifiziert oder gelöscht werden.

_* Law & Order Special Victims Unit dumdumm sound *_

## DSA erzeugen

Es benötigt zwei Ressourcen, damit erfolgreich ein Dogu Service Account (DSA) benutzt werden kann:

1. Existenz einer Service Account Request CR (`SARE`)
   - dies entspricht einem DSA Consumer
2. Existenz einer Service Account Producer CR (`SAPR`)
   - dies entspricht einem DSA Producer

Wenn die Anforderung von SARE und SAPR in dem jeweiligen Feld `.Spec.Producer` übereinstimmen, dann erzeugt der Service
Account Operator durch API-Call auf den DSA Producer ein Credential und legt dieses in einem wohlbekannten Secret ab.

Natürlich können viele verschiedene Dogus oder Komponenten bei einem DSA Producer einen DSA erbitten, sodass einer SAPR
CR mehrere SARE CRs gegenüber stehen. Die Zuordnung eines SAREs zu einem SAPR bleibt davon aber unbeschadet. Die
folgende Grafik zeigt wie Service Account Operator, DSA Consumer und Producer im Verhältnis stehen.

![Ein einzelner DSA Producer (unbestimmt, ob Dogu oder Component) enthält einen SAPR mit dem Producernamen "gareth". Demgegenüber stehen sowohl ein DSA Consumer-Dogu und eine DSA Consumer-Komponente, die ihrerseits einen DSA mit dem Producernamen "gareth" erfragen. Der Service Account Operator erkennt die Übereinstimmung und erzeugt jeweils ein Secret, welches das DSA zwischen Consumer und Producer entspricht.](images/relationship_sare_sapr.drawio.png "DSA-Beziehung zwischen unterschiedlichen DSA Consumern und einem DSA Producer")

Wenn ein SARE existiert, zu dem der Service Account Operator keinen SAPR finden kann, dann wird der SARE mit einer aussagekräftigen Condition versehen. Diese SARE wird erst dann wieder vom Operator betrachtet, wenn eine entsprechende SAPR auf den Cluster angewendet wird.

## DSA modifizieren



## DSA löschen
