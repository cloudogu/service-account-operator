# Operative Szenarien verstehen

Dogu Service Accounts nach der Dogu-API v3 verfolgen im Cloudogu EcoSystem (CES) ein ähnliches Ziel wie die Service
Accounts der Dogu-API v2, hier jedoch erweitert und mit leicht anderer Ausrichtung. In Dogu-API v2 waren auch
Hilfscontainer wie Datenbanken Dogus, die häufig für die Datenhaltung einen Service Account zur Sicherung und
Dogu-Trennung notwendig machten.

Mit der Dogu-API v3 ist dies für Hilfscontainer nicht mehr nötig, da Dogu Helm-Charts eigene, weitere beliebige
Container hervorbringen können, für deren Zugriff kein CES-übergreifender Mechanismus nötig ist. Allerdings ist es
weiterhin möglich, das Dogus miteinander kommunizieren können. Hierzu werden weiterhin DSA benötigt. Darüber hinaus
gelten DSA nicht nur für Dogus, sondern auch für CES-Komponenten. Beide können sowohl als DSA-Consumer und/oder als DSA
Producer auftreten.

Dieses Dokument beschreibt Szenarien, in denen DSAs erzeugt, modifiziert oder gelöscht werden.

_* Law & Order Special Victims Unit dumdumm sound *_

## DSA erzeugen

Im Gegensatz zur DSA-Modifikation gibt es für eine DSA-Erzeugung nur ein einziges Szenario. Es benötigt zwei Ressourcen,
damit erfolgreich ein Dogu Service Account (DSA) benutzt werden kann:

1. Existenz einer Service Account Request CR (`SARE`)
   - dies entspricht einem DSA-Consumer
2. Existenz einer Service Account Producer CR (`SAPR`)
   - dies entspricht einem DSA-Producer

Wenn die Anforderung von SARE und SAPR in dem jeweiligen Feld `.spec.producer` übereinstimmen, dann erzeugt der Service
Account Operator durch API-Call auf den DSA-Producer ein Credential und legt dieses in einem wohlbekannten Secret ab.

Natürlich können viele verschiedene Dogus oder Komponenten bei einem DSA-Producer einen DSA erbitten, sodass einer SAPR
CR mehrere SARE CRs gegenüber stehen. Die Zuordnung eines SAREs zu einem SAPR bleibt davon aber unbeschadet. Die
folgende Grafik zeigt wie Service-Account-Operator, DSA-Consumer und Producer im Verhältnis stehen.

![Ein einzelner DSA-Producer (unbestimmt, ob Dogu oder Component) enthält einen SAPR mit dem Producernamen "gareth". Demgegenüber stehen sowohl ein DSA-Consumer-Dogu und eine DSA-Consumer-Komponente, die ihrerseits einen DSA mit dem Producernamen "gareth" erfragen. Der Service-Account-Operator erkennt die Übereinstimmung und erzeugt jeweils ein Secret, welches das DSA zwischen Consumer und Producer entspricht.](images/relationship_sare_sapr.drawio.png "DSA-Beziehung zwischen unterschiedlichen DSA-Consumern und einem DSA-Producer")

Wenn ein SARE existiert, zu dem der Service-Account-Operator keinen SAPR finden kann, dann wird der SARE mit einer
aussagekräftigen Condition versehen. Diese SARE wird erst dann wieder vom Operator betrachtet, wenn eine entsprechende
SAPR auf den Cluster angewendet wird.

## DSA modifizieren

Während eine Erzeugung eines DSA nur einen einzigen Prozess enthält, gibt es mehrere unterschiedliche Szenarien, die zu
einer Änderung eines DSA führen können.

### Änderung von DSA-Parametern

Das SARE-Feld `.spec.params` dient dazu, die Datenablage zu beeinflussen. Bspw. bei einer Datenbank könnte dies ein
UTF-8-Dialect sein, bei einem Webserver ein besonderer URL-Startpfad. Wie die Parameter verwendet werden, hängt stark
von dem DSA-Producer ab.

Wenn der DSA-Producer dies unterstützt, so führt die Änderung von `.spec.params` in einem bestehenden SARE zu einer
erneuten Reconciliation des Service-Account-Operators, in der der DSA-Producer dazu angehalten wird, die bestehende
Datenhaltung zu dem verantwortlichen DSA-Consumer zu ändern.

Es ist _möglich_, dass das vorher erzeugte Secret sich nicht ändert.

Gleichermaßen ist es nicht ausgeschlossen, dass dem Datenbestand bei Erfolg ein neues Credential ergibt, das wiederum zu
einer Aktualisierung des Secrets führt. Wenn sich hierbei die Struktur des Secrets ändert (Anzahl der Werte, Namen der
Werteschlüssel, Art der Verschlüsselung/Kodierung, usw), so muss gleichzeitig der Service-Account-Consumer diese neue
Struktur verarbeiten können. Der DSA-Consumer muss auf diese Änderung reagieren (bei EnvVars ein Pod-Neustart, bei
Dateimounts ein neues Auslesen der Datei etc).

### Secret-Rotation

In der Vergangenheit ließen sich v2-Dogu-Service-Accounts nicht aktualisieren. Wenn ein Zugang ge_leak_t wäre, hätte
dieser mit manuellem Aufwand sowohl im Consumer- als auch im Producer-Dogu dem bestehenden Datenbestand neu gesetzt
werden müssen.

Damit dies nicht passiert, sieht die Dogu-API v3 vor, dass DSA-Credentials rotiert werden können. Da es sich um
technische Konten zwischen zwei Anwendungen handelt, deren Zugangsdaten sich kein Mensch merken muss, so lassen sich
diese Credentials sogar regelmäßig und häufig rotieren. Hierzu werden übliche Cron-Ausdrücke verwendet:

```goregexp
^(@(annually|yearly|monthly|weekly|daily|hourly)|(((\d+,)*\d+|(\d+(\/|-)\d+)|\*)\s?){5,6})$
```

Dies kann auch manuell angestoßen werden, z. B. im Falle eines Datenleaks.

Für eine Rotation muss allerdings der DSA-Producer eine Aktualisierung von Credentials unterstützen. Bei Erfolg wird
garantiert das DSA-Secret aktualisiert, d. h. der DSA-Consumer muss auf diese Änderung reagieren (bei EnvVars ein
Pod-Neustart, bei Dateimounts ein neues Auslesen der Datei etc).

### DSA-Producer wird deinstalliert

Sollte der DSA-Producer deinstalliert werden, so werden auch alle SAPR CRs entfernt. Dies entspricht inhaltlich einer
Auflösung jener Übereinkunft, die unter [DSA erzeugen](#dsa-erzeugen) beschrieben wurde. In diesem Fall wird auch das
DSA-Secret gelöscht und der DSA-Consumer muss auf diese Änderung reagieren.

### Änderungen während eines Upgrades von DSA-Producern

Es liegt im Bereich des Möglichen, dass ein Upgrade seitens des Tools, welches als DSA-Producer fungiert, eine
Veränderung der Credentials oder der Ablage erfordert, bspw. eine Verschlüsselung gilt als nicht mehr sicher und der
Datenbestand muss neu verschlüsselt werden.

Unter diesem Umstand kann analog zum Abschnitt [Änderung von DSA-Parametern](#änderung-von-dsa-parametern) das
DSA-Secret aktualisiert werden, worauf der DSA-Consumer ebenso reagieren muss.

---

> TODO: hier bin ich mir nicht mehr sicher, welche API das sein soll. Die HTTP-API ggü. dem SA-Op muss doch nur diesem
> bekannt sein. Diese wird auch nur dann interessant, wenn der nächste Änderungs-/Lösch-Call aufkommt. Dann weiß aber
> der SA-Op doch, wie die API gestrickt sein muss, da der Producer dies doch bereits vorher dokumentiert.

Gleiches gilt, wenn der Producer seine eigene API äääääh....

## DSA löschen

Wenn ein DSA-Consumer (z. B. durch technologischen Wandel) gegenüber dem DSA-Producer keinen DSA mehr benötigt, so
sollte der SARE in einem Upgrade gelöscht werden. Dieser Löschvorgang entspricht inhaltlich einer Auflösung jener
Übereinkunft, die unter [DSA erzeugen](#dsa-erzeugen) beschrieben wurde. In diesem Fall wird auch das DSA-Secret
gelöscht.

Der DSA-Producer muss auf diese Änderung so reagieren, dass sowohl sämtliche Credentials als auch Nutzdaten des
betroffenen DSA-Consumers gelöscht werden.