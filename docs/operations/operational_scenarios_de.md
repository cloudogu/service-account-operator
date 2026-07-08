# Operative Szenarien verstehen

Dogu Service Accounts (DSA) nach der Dogu-API v3 verfolgen im Cloudogu EcoSystem (CES) ein ähnliches Ziel wie die
Service-Accounts der Dogu-API v2, hier jedoch erweitert und mit leicht anderer Ausrichtung. In Dogu-API v2 waren auch
Hilfscontainer wie Datenbanken Dogus, die häufig für die Datenhaltung einen Service Account zur Sicherung und
Dogu-Trennung notwendig machten.

Mit der Dogu-API v3 ist dies für Hilfscontainer nicht mehr nötig, da Dogu Helm-Charts eigene, weitere beliebige
Container hervorbringen können, für deren Zugriff kein CES-übergreifender Mechanismus nötig ist. Allerdings ist es
weiterhin möglich, dass Dogus miteinander kommunizieren. Hierzu werden weiterhin Dogu-API v3 Service Accounts (DSA)
benötigt. Trotz des Namens gelten diese DSAs nicht nur für Dogus, sondern auch für CES-Komponenten, die auf Dogus
oder andere CES-Komponenten per API zugreichen möchten. Beide können sowohl als DSA-Consumer und/oder als DSA-Producer
auftreten, was auch Ringabhängigkeiten erlaubt. Dogus bzw Komponenten können ordentlich zu Ende installiert werden.
Der Service Account Operator sorgt dank des SARE-/SAPR-Mechanismus für die Entkopplung dieser Abhängigkeiten.

Es können viele verschiedene Dogus oder Komponenten bei einem DSA-Producer einen DSA erbitten, sodass einer SAPR-CR
mehrere SARE-CRs gegenüber stehen. Die Zuordnung eines SAREs zu einem SAPR bleibt davon aber unbeschadet. Die
folgende Grafik zeigt, wie Service-Account-Operator, DSA-Consumer und -Producer im Verhältnis stehen.

![Ein DSA-Producer (unbestimmt, ob Dogu oder Component) enthält einn SAPR-Ressource mit dem Producernamen "gareth".
Dem gegenüber stehen ein DSA-Consumer-Dogu und eine DSA-Consumer-Komponente, die ihrerseits einen DSA mit dem
Producernamen "gareth" erfragen. Der Service-Account-Operator erkennt die Übereinstimmung und erzeugt jeweils ein Secret
welches dem DSA zwischen Consumer und Producer entspricht.](images/relationship_sare_sapr.drawio.png
"DSA-Beziehung zwischen unterschiedlichen DSA-Consumern und einem DSA-Producer")

Damit der Prozess der DSA-Erzeugung/Updates/Löschung erfolgreich durchgeführt werden kann, muss das Dogu/die Komponente
die _Service-Account-Producer-API_ implementieren. Diese liegt als [OpenAPI-Spezifikation vor](openapi.yaml).

Dieses Dokument beschreibt Szenarien, in denen DSAs erzeugt, modifiziert oder gelöscht werden.

## DSA erzeugen

Im Gegensatz zur DSA-Modifikation gibt es für eine DSA-Erzeugung nur ein einziges Szenario. Es benötigt zwei Ressourcen,
damit erfolgreich ein DSA benutzt werden kann:

1. Existenz einer Service-Account-Request-CR (`SARE`)
   - dies entspricht einem DSA-Consumer
2. Existenz einer Service-Account-Producer-CR (`SAPR`)
   - dies entspricht einem DSA-Producer

Wenn die Anforderung von SARE und SAPR in dem jeweiligen Feld `.spec.producer` übereinstimmen, dann erzeugt der
Service-Account-Operator per API-Call auf den DSA-Producer Credentials und legt diese in einem wohlbekannten Secret ab.

![Ein Consumer deployt ein SARE zu einem existierenden SAPR. Der Service-Account-Operator erkennt dies und stellt einen
Endpunkt-Request gegenüber einem Service-Account-Producer-Service. Dieser implementiert die Service-Account-Producer-API
aus dem Operator. Der Service ist in der Regel ein Sidecar im Producer. Dieser erzeugt, aktualisiert oder löscht mittels
der eigentlichen Nutzanwendung den gewünschten Datenzustand. Hierbei fällt (nicht bei Delete) ein Credential heraus,
das die API wieder an den Operator zurückgibt. Der Operator schreibt das Credential in das vom SARE genannten Secret und
übereignet dem Consumer das Secret. Der Consumer kann nun den DSA verwenden.](
images/saOperator_calls_dogu_saService.drawio.png "Abbildung des Prozesses wie ein DSA-Consumer einen SARE deployt.
Der Service-Account-Operator erzeugt mittels Producer ein Secret")

Wenn ein SARE existiert, zu dem der Service-Account-Operator keinen SAPR finden kann, wird der SARE mit einer
aussagekräftigen Condition versehen. Dieser SARE wird erst dann wieder vom Operator betrachtet, wenn eine entsprechende
SAPR auf den Cluster angewendet wird.

## DSA modifizieren

Während eine Erzeugung eines DSA nur einen einzigen Prozess enthält, gibt es mehrere unterschiedliche Szenarien, die zu
einer Änderung eines DSA führen können.

### Änderungen in der Producer-API

Bei Änderungen der Producer-API muss die SAPR-CR aktualisiert werden. Diese Änderungen können z. B. Änderungen an der
Endpunkte-URL sein, den DSA-Parametern oder der Struktur, in der Credentials zurückgegeben werden.

Wenn sich daraus beim Producer die DSA-Parameter ändern, muss der DSA-Consumer auch entsprechend angepasst werden,
siehe nächsten Abschnitt.

### Änderung von DSA-Parametern

Das SARE-Feld `.spec.params` dient dazu, die Datenablage oder Berechtigungen zu beeinflussen. Bspw. bei einer Datenbank
könnte dies ein UTF-8-Dialect sein, bei einem Webserver ein URL-Startpfad. Wie die Parameter verwendet werden, hängt vom
DSA-Producer ab.

Wenn der DSA-Producer die Parameter unterstützt, so führt die Änderung von `.spec.params` in einem bestehenden SARE zu
einer erneuten Reconciliation des Service-Account-Operators, in der der DSA-Producer die Möglichkeit hat, die bestehende
Datenhaltung zu dem verantwortlichen DSA-Consumer zu ändern.

Es ist _möglich_, dass das vorher erzeugte Secret sich nicht ändert.

Gleichermaßen ist es nicht ausgeschlossen, dass sich bei Erfolg ein neues Credential ergibt, das wiederum zu
einer Aktualisierung des Secrets führt. Wenn sich hierbei die Struktur des Secrets ändert (Anzahl der Werte, Namen der
Werteschlüssel, Art der Verschlüsselung/Kodierung usw), so muss gleichzeitig der Service-Account-Consumer diese neue
Struktur verarbeiten können.

Der DSA-Consumer sollte auf Änderungen des Secrets zur Laufzeit reagieren, siehe [Reagieren des DSA-Consumers auf eine
Secret-Rotation](#reagieren-des-dsa-consumers-auf-eine-secret-rotation).

### Secret-Rotation

In der Vergangenheit ließen sich v2-Dogu-Service-Accounts nicht aktualisieren. Wenn ein Zugang geleakt wäre, hätte
dieser mit manuellem Aufwand sowohl im Consumer- als auch im Producer-Dogu manuell rotiert werden müssen.

Damit dies nicht passiert, sieht die Dogu-API v3 vor, dass DSA-Credentials rotiert werden können. Da es sich um
technische Konten zwischen zwei Anwendungen handelt, deren Zugangsdaten sich kein Mensch merken muss, so lassen sich
diese Credentials sogar regelmäßig und häufig rotieren. Hierzu werden übliche Cron-Ausdrücke verwendet:

```goregexp
^(@(annually|yearly|monthly|weekly|daily|hourly)|(((\d+,)*\d+|(\d+(\/|-)\d+)|\*)\s?){5,6})$
```

Dies kann auch manuell angestoßen werden, z. B. im Falle eines Datenleaks.

Für eine Rotation muss allerdings der DSA-Producer ein Neu-Ausstellen von Credentials unterstützen. Bei Erfolg wird
garantiert das DSA-Secret aktualisiert, d. h. der DSA-Consumer sollte auf diese Änderung reagieren (siehe [Reagieren
des DSA-Consumers auf eine Secret-Rotation](#reagieren-des-dsa-consumers-auf-eine-secret-rotation)).

Ein einfacher Weg, einmalig solch eine Secret-Rotation anzustoßen, ist die Löschung des genannten Secrets. Der
Service-Account-Operator horcht auf Löschung von Secrets. Handelt sich hierbei um ein DSA-Secret, wird beim Producer
eine Credential-Rotation angestoßen. In der Zeit zwischen Secret-Löschung und Neu-Erzeugung kann ggf. der Consumer mit
dem alten Secret nicht mehr auf den Producer zugreifen, da der Producer evtl. bereits die Credentials ausgetauscht hat.

#### Reagieren des DSA-Consumers auf eine Secret-Rotation

Bisher unklar ist, wie der DSA-Consumer auf Änderungen des Secrets zur Laufzeit reagieren soll.
Bei Env-Vars ist ein Neustart des Pods erforderlich, Änderungen an gemounteten Secrets könnten theoretisch auch jetzt
schon zur Laufzeit erkannt, und die Dateien neu ausgelesen werden.

### DSA-Producer wird deinstalliert

Sollte der DSA-Producer deinstalliert werden, so werden auch alle SAPR-CRs entfernt. Dies entspricht inhaltlich einer
Auflösung jener Übereinkunft, die unter [DSA erzeugen](#dsa-erzeugen) beschrieben wurde. In diesem Fall wird auch das
DSA-Secret gelöscht und der DSA-Consumer muss auf diese Änderung reagieren.

### Interne Änderungen während eines Upgrades von DSA-Producern

Es liegt im Bereich des Möglichen, dass ein Upgrade seitens des Tools, welches als DSA-Producer fungiert, eine
Veränderung der Credentials oder der Ablage erfordert, bspw. eine Verschlüsselung gilt als nicht mehr sicher und der
Datenbestand muss neu verschlüsselt werden.

Der Producer erfordert daher ein Update des DSA-Secrets, kann dies aber nicht selbst veranlassen. Damit dies
programmatisch umgesetzt werden kann, kann analog zum Abschnitt [Änderung von DSA-Parametern](
#änderung-von-dsa-parametern) ein Pseudo-DSA-Parameter im SAPR ergänzt werden, der diese Änderung abbildet.

Alternativ kann natürlich nach dem Update auch eine manuelle Rotation des Secrets veranlasst werden, siehe
[Secret-Rotation](#secret-rotation).

## DSA löschen

Wenn ein DSA-Consumer (z. B. durch technologischen Wandel) gegenüber dem DSA-Producer keinen DSA mehr benötigt, so
sollte der SARE in einem Upgrade gelöscht werden. Dieser Löschvorgang entspricht inhaltlich einer Auflösung jener
Übereinkunft, die unter [DSA erzeugen](#dsa-erzeugen) beschrieben wurde. In diesem Fall wird auch das DSA-Secret
gelöscht.

Der DSA-Producer muss auf diese Änderung so reagieren, dass sowohl sämtliche Credentials als auch Nutzdaten des
betroffenen DSA-Consumers gelöscht werden. Dieses Vorgehen spart nicht nur Ressourcen, sondern entspricht auch unserem Datenschutzverständnis: Gelöschte Daten können nicht unberechtigt eingesehen oder weitergegeben werden.