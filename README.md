# MailShield

E-Mail-↔-Telegram-Brücke für kleine Teams. Empfängt eingehende Mails, leitet sie als Forum-Topics in eine persönliche Telegram-Supergruppe (ein Topic pro externem Kontakt) und stellt Antworten als echte DKIM-signierte E-Mail zu — ganz ohne Mail-Client.

**Live:** `support@shk.solutions` · VPS `82.165.47.33` · Go + Docker

---

## Funktionsweise

```
Internet ──SMTP──▶ MailShield ──▶ SPF-Prüfung ──▶ Telegram-Forum-Topic
                                                        │
                                                 Nutzer antwortet im Topic
                                                        │
                                                        ▼
Internet ◀──SMTP── direkter MTA ◀── DKIM-Signatur ◀── ReplyService
```

1. Eingehende E-Mail trifft auf Port 25 ein (MX → VPS)
2. SPF wird geprüft, Risiko 1–10 bewertet
3. Ist der Absender neu → wird ein **Topic** in der Supergruppe des Nutzers angelegt
4. Die E-Mail wird als formatierte HTML-Nachricht in diesem Topic gepostet
5. Antwortet der Nutzer im Topic → sendet MailShield sie als echte E-Mail an den ursprünglichen Absender

---

## Architektur

Hexagonal (Ports & Adapters). Die Domänenlogik kennt weder SMTP-Framing noch Telegram-Chat-IDs noch SQL — nur Schnittstellen.

```
cmd/mailshield/         — Composition Root
internal/
  core/
    model.go            — Domänentypen (User, ParsedEmail, ConversationID…)
    ports.go            — sämtliche Schnittstellen (driving + driven)
    app/
      ingest.go         — MailIngestor-Use-Case
      reply.go          — ReplyService-Use-Case
  adapters/
    inbound/smtp/       — SMTP-Server (mhale/smtpd)
    outbound/dns/       — SPF-Verdicter via net.LookupTXT
    outbound/mailer/    — direkte MTA-Zustellung + DKIM (emersion/go-msgauth)
    sqlite/             — ConversationStore + UserRegistry + TopicIndex + AdminStore
    telegram/           — Forum-Topic-Notifier + Update-Poller
    fake/, inmem/       — Test-Doubles
keys/                   — DKIM-PEM-Dateien (gitignored)
```

**Driving Ports:** `MailIngestor`, `ReplyService`
**Driven Ports:** `Notifier`, `MailSender`, `Verdicter`, `ConversationStore`, `UserRegistry`, `TopicIndex`

---

## Voraussetzungen

- Go 1.21+
- VPS mit **offenem Port 25** (bei den meisten Consumer-ISPs blockiert)
- Domain-DNS:

| Eintrag | Wert |
|--------|-------|
| `MX` | `shk.solutions.` → VPS-IP |
| `TXT` (SPF) | `v=spf1 ip4:<VPS_IP> -all` |
| `TXT` (DKIM) | `mail._domainkey` → Public Key |
| `PTR` | VPS-IP → `shk.solutions` |

---

## Umgebungsvariablen

| Variable | Erforderlich | Standard | Beschreibung |
|----------|----------|---------|-------------|
| `TG_TOKEN` | ✓ | — | Telegram-Bot-Token |
| `TG_ADMIN_ID` | ✓ | — | Telegram-`user_id` des Admins, der Postfächer anlegt |
| `BIND_ADDR` | | `0.0.0.0:2525` | SMTP-Listen-Adresse im Container |
| `HOSTNAME` | | `shk.solutions` | SMTP-Hostname / MAIL-FROM-Domain |
| `DB_PATH` | | `mailshield.db` | Pfad zur SQLite-Datenbank |
| `DKIM_KEY_PATH` | | `keys/dkim_private.pem` | privater DKIM-RSA-Schlüssel (PEM) |
| `DKIM_SELECTOR` | | `mail` | DKIM-Selector |
| `SMTP_RELAY_HOST` | | — | Hostname des ausgehenden SMTP-Relays (z. B. `smtp-relay.brevo.com`) |
| `SMTP_RELAY_PORT` | | `587` | Relay-Port (STARTTLS) |
| `SMTP_RELAY_USER` | | — | Relay-SMTP-Login |
| `SMTP_RELAY_PASS` | | — | Relay-SMTP-Passwort |

`TG_TOKEN` und `TG_ADMIN_ID` liegen in `.env`; alles andere in `docker-compose.yml`. Um die eigene `TG_ADMIN_ID` herauszufinden, dem Bot einfach schreiben — er antwortet mit der `chat_id`.

**Ausgehende Zustellung ist optional.** Ohne `SMTP_RELAY_HOST` startet der Dienst normal und empfängt Mails wie gewohnt; Antworten aus Telegram werden dann still übersprungen (`WARN: outbound relay not configured`). Relay-Zugangsdaten in `.env` eintragen, sobald der Versand aktiviert werden soll.

---

## Lokaler Betrieb

```bash
# 1. Abhängigkeiten installieren
go mod download

# 2. .env mit Bot-Token und Admin-user_id anlegen
printf 'TG_TOKEN=<your_token>\nTG_ADMIN_ID=<your_user_id>\n' > .env

# 3. Starten
export $(cat .env | xargs)
go run ./cmd/mailshield

# 4. Test-E-Mail senden (benötigt Bun)
HOST=localhost PORT=2525 bun bun/send.ts

# 5. Tests
go test ./...
```

---

## Deployment auf dem VPS

**Verzeichnisstruktur auf dem Server:**

```
/srv/mailshield/
├── docker-compose.yml
├── Dockerfile
├── .env                  ← TG_TOKEN + TG_ADMIN_ID — manuell anlegen
├── keys/
│   └── dkim_private.pem  ← bereits auf dem VPS aus dem Deliverability-Setup
└── data/                 ← wird von Docker angelegt; enthält mailshield.db
```

**Synchronisieren und starten:**

```bash
# Vom lokalen Rechner aus
rsync -av --exclude='.env' --exclude='data/' \
  ./go_mail_serv/ root@82.165.47.33:/srv/mailshield/

# Auf dem VPS
ssh root@82.165.47.33
cd /srv/mailshield
printf 'TG_TOKEN=<token>\nTG_ADMIN_ID=<user_id>\n' > .env   # nur falls noch nicht vorhanden
docker compose up -d --build
docker compose logs -f
```

**Start mit leerer Datenbank** (einmaliger Reset — die Seed-User sind weg;
alle Postfächer werden nun zur Laufzeit über Telegram angelegt):

```bash
cd /srv/mailshield
docker compose down
rm -f ./data/mailshield.db*          # .db, .db-wal, .db-shm
docker compose up -d --build
```

---

## Admin-Panel (Telegram)

Die gesamte Postfachverwaltung läuft über den Bot in einem **Direktchat mit dem Admin**
(dem Account, dessen `user_id` mit `TG_ADMIN_ID` übereinstimmt). Keine Shell, kein SQL.

| Befehl | Aktion |
|---------|--------|
| `/adduser email [name]` | Postfach anlegen → Bot liefert einen **Bind-Code** |
| `/bind CODE` | *(innerhalb der Ziel-Supergruppe ausführen)* verknüpft dieses Postfach mit der Gruppe |
| `/users` | Postfächer und ihren Bind-Status auflisten |
| `/setchat email chat_id` | chat_id eines Postfachs manuell setzen (Fallback) |
| `/deluser email` | Postfach entfernen |
| `/help` | Befehlsübersicht anzeigen |

Bind-Codes sind Einmal-Codes, erzeugt mit `crypto/rand`, und werden bis zur
Einlösung in der Tabelle `bind_codes` gespeichert.

---

## Onboarding eines Nutzers (Forum Topics)

Jeder Nutzer erhält eine eigene **Supergruppe mit aktivierten Topics**. Der Bot legt
automatisch ein Topic pro externem Kontakt an. Die gespeicherte chat_id eines Nutzers
**muss** die ID der Supergruppe sein (negativ, `-100…`) — deshalb erfolgt das Binden
innerhalb der Gruppe.

**Schritte:**

1. Eine Telegram-Supergruppe anlegen und **Gruppeneinstellungen → Topics ✓** aktivieren
2. `@your_bot` als Admin hinzufügen → Rechte **Nachrichten löschen** und **Topics verwalten** vergeben
3. Admin (im DM mit dem Bot): `/adduser fima@shk.solutions Fima` → Bot antwortet mit einem Code
4. Beliebiges Mitglied in der Supergruppe: `/bind <code>` → der Bot erfasst die Gruppen-ID und verknüpft sie

```
Admin (DM mit dem Bot):
  /adduser fima@shk.solutions Fima
  → ✅ Mailbox created. Bind code: A1B2C3
     Send /bind A1B2C3 in Fima's supergroup.

In Fimas Supergruppe:
  /bind A1B2C3
  → ✅ fima@shk.solutions linked to this group
```

Kein Neustart nötig — der Poller liest die DB pro Anfrage. Der `/bind`-Schritt funktioniert
auch als manueller Fallback via `/setchat email <chat_id>`, falls die Gruppen-ID bereits
bekannt ist (die Zahl nach `#` in der `web.telegram.org`-URL).

---

## Nutzer und Aliasse

Die Datenbank **startet leer**. Postfächer werden zur Laufzeit vom Admin per
`/adduser` angelegt; danach ist die Datenbank die einzige Quelle der Wahrheit. Die
Autorität des Admins kommt aus `TG_ADMIN_ID` (env), unabhängig von der `users`-Tabelle
— so kann der Admin alles auf einer frischen Datenbank bootstrappen. Zu beachten: auch
der Admin braucht ein eigenes `/adduser`, um Mails **empfangen** zu können.

| Adresse | Typ | Beschreibung |
|---------|------|-------------|
| `boris@shk.solutions` | Nutzer | angelegt via `/adduser`, an eine Supergruppe gebunden |
| `fima@shk.solutions` | Nutzer | angelegt via `/adduser`, an eine Supergruppe gebunden |
| `team@shk.solutions` | Alias | Fan-out → an alle aktiven Nutzer (konfiguriert in `main.go`) |

---

## Observability & Hardening (Etappe 7)

### Strukturiertes Logging

Sämtliche Log-Ausgaben sind JSON (`log/slog` mit `JSONHandler`). Jede Zeile ist ein
maschinenlesbares Objekt — leicht mit `jq` zu filtern oder an einen Log-Aggregator
weiterzuleiten:

```bash
# auf dem VPS — nach Level filtern
docker compose logs -f | jq 'select(.level=="ERROR")'

# Zustellversuche beobachten
docker compose logs -f | jq 'select(.msg=="delivering")'
```

Typische Startausgabe:

```json
{"time":"...","level":"INFO","msg":"sqlite ready","path":"/app/data/mailshield.db"}
{"time":"...","level":"INFO","msg":"telegram authorised","username":"YourBot"}
{"time":"...","level":"INFO","msg":"smtp listening","addr":"0.0.0.0:2525"}
{"time":"...","level":"INFO","msg":"MailShield started","bind":"0.0.0.0:2525","domain":"shk.solutions","admin":5238002828}
```

### Netzwerk-Timeouts

Jeder blockierende Netzwerkaufruf hat jetzt eine explizite Deadline — ein hängender
MX-Server oder eine träge Telegram-API können den Prozess nicht mehr einfrieren:

| Aufruf | Timeout |
|------|---------|
| DNS `LookupTXT` (SPF-Prüfung) | 10 s |
| DNS `LookupMX` (ausgehende Zustellung) | 10 s |
| TCP-Dial zum entfernten MTA | 15 s |
| Kompletter Ingest-Pipeline pro E-Mail | 30 s |

### Panic Recovery

Der Telegram-Update-Poller umschließt jede eingehende Nachricht mit einem
`defer recover()`. Ein fehlerhaftes Update, das einen Panic auslöst, wird als
`ERROR` geloggt und übersprungen — der Poller verarbeitet die folgenden Nachrichten
weiter.

### golangci-lint in der CI

`.golangci.yml` aktiviert `errcheck`, `govet`, `staticcheck`, `ineffassign`, `misspell`,
`gosec`, `bodyclose` und `noctx`. Der Lint-Job läuft bei jedem Push/PR, bevor das
Docker-Image gebaut wird.

---

## Roadmap

| # | Beschreibung | Status |
|---|-------------|--------|
| 0 | Deliverability-Spike — PTR, SPF, DKIM auf dem VPS | ✅ erledigt |
| 1 | SMTP-Inbound + Telegram-Benachrichtigung | ✅ erledigt |
| 2 | Antwort aus Telegram → ausgehende E-Mail | ✅ erledigt |
| 3 | SQLite-Persistenz | ✅ erledigt |
| 4 | Multi-User-Routing + `team@`-Alias-Fan-out | ✅ erledigt |
| 5 | Telegram Forum Topics (ein Topic pro externem Kontakt) | ✅ erledigt |
| 6 | Admin-Panel via Telegram (`/adduser`, Bind-Codes, Bootstrap bei leerer DB) | ✅ erledigt |
| 7 | Hardening — slog JSON, Context-Timeouts, Panic Recovery, golangci-lint-CI | ✅ erledigt |

---

## Kernabhängigkeiten

| Paket | Rolle |
|---------|------|
| `mhale/smtpd` | SMTP-Server |
| `jhillyerd/enmime` | MIME-Parsing |
| `go-telegram-bot-api/v5` | Telegram Bot API |
| `emersion/go-msgauth` | DKIM-Signierung |
| `modernc.org/sqlite` | reines Go-SQLite (CGO_ENABLED=0) |
