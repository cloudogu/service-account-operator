# Allgemeine Informationen über Service-Accounting

Service Accounts nach Dogu-API v3 beschreiben einen Mechanismus, der übergreifend über Dogus und Komponenten
technische Zugänge ermöglicht. Hierbei fungiert der Service-Account-Operator als strikter Vermittler zwischen Producer
und Consumer. 

Ein **Producer** stellt eine Service-Account-Producer (SAPR) _Custom-Resource_ zur Verfügung, die seine eigene API
zur Erzeugung von Zugängen beschreibt. Da jede Software unterschiedliche Zugangsnotwendigkeiten besitzt,
beschreibt sie auch, wie die Zugangsdaten inhaltlich aufgebaut sind. Z. B. könnte ein Producer lediglich Kontoname und
Passphrase zurückliefern, während ein anderer zusätzlich Schema- oder URL-Informationen für die Adressierung des Zugangs
benötigt und auch diese bereitstellt.

Producer müssen hierbei eine [Service-Account-Producer-API](openapi.yaml) implementieren und im jeweiligen Softwareteil
(Dogu oder Komponente) zur Laufzeit anbieten. Diese API darf aus Sicherheitsgründen ausschließlich vom
Service-Account-Operator verwendet werden.

**Consumer** hingegen beschreiben ihren Wunsch nach einem Service Account bei einem bestimmten Producer
und in welchem Secret dieser Service Account vom Service-Account-Operator abgelegt werden soll. 

![Ein DSA-Producer (unbestimmt, ob Dogu oder Component) enthält einen SAPR-Ressource mit dem Producernamen "gareth".
Dem gegenüber stehen ein DSA-Consumer-Dogu und eine DSA-Consumer-Komponente, die ihrerseits einen DSA mit dem
Producernamen "gareth" erfragen. Der Service-Account-Operator erkennt die Übereinstimmung und erzeugt jeweils ein Secret
welches dem DSA zwischen Consumer und Producer entspricht.](images/relationship_sare_sapr.drawio.png
"DSA-Beziehung zwischen unterschiedlichen DSA-Consumern und einem DSA-Producer")

## Beispiel-Ressourcen

Das [K8s-Samples-Repo](https://github.com/cloudogu/k8s-ecosystem-samples/tree/main/serviceaccount) enthält Beispiele,
wie SAPR-CRs (für Producer) und SARE-CRs (für Consumer) aussehen können.
Dabei muss sich ein Consumer an die Struktur des Producers halten.

## Migration von Dogu V2

Wird ein Consumer-Dogu von Dogu V2 auf V3 migriert, übernimmt der Service-Account-Operator dessen bestehende
Service-Accounts, statt beim Producer neue anzufordern. Quelle ist das **Migration-Secret** `<doguname>-migration` im
Namespace des SARE. Es wird vom Dogu-Operator erzeugt und auch wieder gelöscht; der Service-Account-Operator liest es
nur.

### Auslöser

Der Operator übernimmt die Credentials aus dem Migration-Secret, wenn alle folgenden Bedingungen erfüllt sind:

- Der SARE trägt das Label `k8s.cloudogu.com/dogu.name`. Dessen Wert ergibt den Namen des Migration-Secrets.
- Für den SARE existiert noch **kein** Service-Account-Secret.
- Der zugehörige SAPR existiert.
- Das Migration-Secret existiert und enthält mindestens einen Key mit dem Präfix `<spec.producer>_` des SARE.

Ist eine dieser Bedingungen nicht erfüllt, fordert der Operator die Credentials wie gewohnt beim Producer an. Ein bereits
vorhandenes Service-Account-Secret wird nie aus dem Migration-Secret überschrieben.

### Ergebnis

Bei Erfolg schreibt der Operator das Service-Account-Secret aus den Daten des Migration-Secrets, **ohne** den Producer aufzurufen.

- Condition `ServiceAccountReady=True` mit Reason `ServiceAccountMigrated`
- `status.secretRef` zeigt auf das Service-Account-Secret
- `status.lastRotation` wird nicht gesetzt, denn eine Migration ist keine Rotation
- Event vom Typ `Normal` mit Action `ServiceAccountMigrated`; die Note nennt Consumer und Migration-Secret

### Fehlerverhalten

Fehlt im Migration-Secret ein Key, den der SAPR in `spec.http.return` deklariert:

- wird kein Service-Account-Secret geschrieben und der Producer nicht aufgerufen, auch nicht als Fallback,
- erhält der SARE die Condition `ServiceAccountReady=False` mit Reason `MigrationFailed`
  - die Message nennt das Migration-Secret und die fehlenden Keys (z. B. `nexus_repository`),
- wird ein Event vom Typ `Warning` mit Action `MigrationFailed` erzeugt,
- wird der Reconcile mit Backoff wiederholt.

Andere Fehler beim Lesen des Migration-Secrets führen zu Reason `ServiceAccountFailed` und ebenfalls zu einer Wiederholung mit Backoff.

### Anforderung an Producer

Der Service-Account-Operator ruft den Producer bei einer Migration nicht auf und behandelt migrierte Service-Accounts danach wie alle anderen.
Er fragt Service-Accounts beim Producer unter `spec.consumer` des SARE an (z. B. `baseline`), in der Regel also unter demselben Namen wie in Dogu V2.
Producer müssen ihre V2-Accounts deshalb unter diesem Namen weiterführen.

Andernfalls schickt der nächste Reconcile mit vorhandenem Service-Account-Secret (z. B. nach einer Änderung der SARE-Spec oder einem Neustart des Operators) eine Anfrage für einen Account an den Producer, den dieser nicht kennt.
Der legt einen neuen Account an, und der Operator überschreibt die migrierten Credentials damit.
Der alte V2-Account bleibt beim Producer verwaist und wird beim Löschen des SARE nicht entfernt.
