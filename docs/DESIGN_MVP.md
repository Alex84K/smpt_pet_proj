# 📐 Go-MailShield — MVP-Design (vor Beginn der Programmierung)

> **Ziel:** die Architekturentscheidung **vor dem Schreiben von Code** festhalten.
> **Methode:** das **C4**-Modell (Context → Containers → Components), Datenfluss-
> diagramme **(DFD)** und **Sequence**-Diagramme.
> **Status:** MVP-Design · **Datum:** 2026-07-24
> **Kontext:** Dieses Dokument ist ein MVP-Ausschnitt der Zielarchitektur aus
> [`ARCHITECTURE.md`](./ARCHITECTURE.md) (Ports & Adapters). Hier ist das
> **Minimum, das wir zuerst bauen**, beschrieben; das Gesamtbild und die Begründungen der Entscheidungen stehen dort.

---

## Inhaltsverzeichnis

1. [Was das MVP macht](#1-was-das-mvp-macht)
2. [Grenzen des MVP (Scope)](#2-grenzen-des-mvp-scope)
3. [C4 · Ebene 1 — System Context](#3-c4--ebene-1--system-context)
4. [C4 · Ebene 2 — Containers](#4-c4--ebene-2--containers)
5. [C4 · Ebene 3 — Components](#5-c4--ebene-3--components)
6. [DFD · Datenflussdiagramme](#6-dfd--datenflussdiagramme)
7. [Sequenzdiagramme](#7-sequenzdiagramme)
8. [Datenmodell (SQLite)](#8-datenmodell-sqlite)
9. [Externe Schnittstellen und Verträge](#9-externe-schnittstellen-und-verträge)
10. [Konfiguration](#10-konfiguration)
11. [Fertigstellungskriterien des MVP (Definition of Done)](#11-fertigstellungskriterien-des-mvp-definition-of-done)
12. [Annahmen und Risiken](#12-annahmen-und-risiken)

---

## 1. Was das MVP macht

In einem Satz: **Eine E-Mail an die Adresse eines Mitarbeiters landet für ihn in
Telegram mit einem Sicherheits-Badge; die Antwort aus Telegram wird als E-Mail
von seiner Adresse im selben Thread verschickt.**

End-to-End-Szenario des MVP:

1. Ein externer Absender schickt eine E-Mail an `boris@shk.solutions`.
2. MailShield nimmt sie per SMTP an, parst MIME, prüft **SPF + DKIM**.
3. Routet nach `RCPT TO` → findet den Mitarbeiter → erstellt ein **Topic** in
   seiner persönlichen Telegram-Gruppe und postet die E-Mail + das Verdict.
4. Boris antwortet **direkt im Topic**.
5. MailShield stellt die ausgehende E-Mail zusammen (`From: boris@`,
   Threading-Header), **signiert sie mit DKIM** und sendet sie über den Relay.
6. Der gesamte Zustand (Benutzer, Konversationen, Threads) liegt in **SQLite**.

---

## 2. Grenzen des MVP (Scope)

| Im MVP enthalten ✅ | NICHT im MVP enthalten ❌ (verschoben in [`ARCHITECTURE.md`](./ARCHITECTURE.md)) |
| :--- | :--- |
| SMTP-Empfang + MIME-Parsing | Phishing-/URL-Heuristiken |
| Analyse **SPF + DKIM** (verify) | Anhangs-Scanner (SHA-256, doppelte Dateiendungen) |
| Routing nach Empfänger (Multi-User) | AI/LLM-Risk-Score |
| Telegram: persönliche Gruppen + Topics (**Variante 2**) | DMARC-Enforcement (nur Ergebnis wird festgehalten) |
| Antwort aus Telegram → ausgehende E-Mail | Direct-MTA-Zustellung (im MVP nur Relay) |
| DKIM-Signatur für Ausgehendes | HTTP-API / Custom-Client |
| Threading (`In-Reply-To`/`References`) | Self-Service-Onboarding (`/link`) |
| Speicher **SQLite** | Webhook (im MVP: Long-Polling) |
| Benutzerregister aus Konfig/DB | Catch-all (unbekannte Adresse → `550`) |
| Autorisierung pro Benutzer bei Antworten | Anhänge aus der E-Mail → nach Telegram (im MVP nur Text) |

**Das zentrale Ziel des MVP** ist es, den End-to-End-Zyklus E-Mail ↔ Telegram
mit korrektem Threading und persistentem Zustand über Neustarts hinweg
nachzuweisen. Alles Weitere sind Features obendrauf.

---

## 3. C4 · Ebene 1 — System Context

Wer nutzt das System und womit kommuniziert es nach außen.

```mermaid
flowchart TB
    sender["👤 [Person]<br/>Externer Absender<br/>client@acme.com"]
    emp["👤 [Person]<br/>Mitarbeiter<br/>Fima / Boris"]

    subgraph sys["[Software System] Go-MailShield"]
        core["Filternde E-Mail ↔ Telegram-Brücke<br/>Empfang • Analyse • Routing • Antwort"]
    end

    tg["[External System]<br/>Telegram Bot API"]
    dns["[External System]<br/>DNS (SPF/DKIM/MX)"]
    relay["[External System]<br/>SMTP-Relay / MX des Empfängers"]

    sender -- "SMTP :25 (E-Mail)" --> sys
    sys -- "sendMessage / createForumTopic" --> tg
    tg -- "getUpdates (Antwort des Mitarbeiters)" --> sys
    emp -- "liest/antwortet in Telegram" --> tg
    sys -- "LookupTXT / verify" --> dns
    sys -- "ausgehende E-Mail (SMTP)" --> relay

    style sys fill:#1f6feb22,stroke:#1f6feb
```

**Akteure und externe Systeme:**

| Element | Rolle |
| :--- | :--- |
| Externer Absender | Schickt eine E-Mail an eine Domain-Adresse; erhält eine Antwort. |
| Mitarbeiter (Fima/Boris) | Liest Eingehendes und antwortet **innerhalb von Telegram**. |
| Telegram Bot API | UI-Transport: Zustellung von Eingehendem, Empfang von Antworten. |
| DNS | Quelle für SPF/DKIM-Einträge (verify) und MX (bei Direct-Zustellung, außerhalb des MVP). |
| SMTP-Relay / MX | Letzte Meile der Zustellung von Ausgehendem. |

---

## 4. C4 · Ebene 2 — Containers

Deploybare Einheiten. Das MVP ist bewusst kompakt gehalten: **ein Go-Prozess +
eine DB-Datei**.

```mermaid
flowchart TB
    sender["👤 Externer Absender"]
    emp["👤 Mitarbeiter (Telegram)"]

    subgraph host["[Deployment] VPS · Docker Compose"]
        app["[Container] MailShield App<br/><i>Go-Binary (CGO_ENABLED=0)</i><br/>SMTP-Listener + Kern + Adapter<br/>+ Telegram-Long-Poller"]
        db[("[Container/Datastore]<br/>SQLite<br/><i>Datei mailshield.db (Volume)</i>")]
        app -- "database/sql<br/>modernc.org/sqlite (WAL)" --> db
    end

    tg["[External] Telegram Bot API"]
    dns["[External] DNS"]
    relay["[External] SMTP-Relay"]

    sender -- "SMTP :25→2525" --> app
    app -- "HTTPS Bot API" --> tg
    emp -- "Chat" --> tg
    app -- "UDP/TCP 53" --> dns
    app -- "SMTP :587/25" --> relay

    style host fill:#8b5cf611,stroke:#8b5cf6
    style app fill:#1f6feb22,stroke:#1f6feb
```

**Container:**

| Container | Technologie | Zweck |
| :--- | :--- | :--- |
| **MailShield App** | Go (eine statische Binary) | Die gesamte Domäne + Adapter in einem Prozess; Concurrency über Goroutinen/Channels. |
| **SQLite** | `modernc.org/sqlite`, WAL | Dauerhafter relationaler Speicher (users/conversations/messages/verdicts). |
| Telegram Bot API | extern | UI-Transport. |
| DNS | extern | SPF/DKIM-Verify. |
| SMTP-Relay | extern | Zustellung von Ausgehendem (Deliverability). |

> Im MVP gibt es **kein** Redis — der Zustand ist primär relational und liegt
> in SQLite (siehe §13 `ARCHITECTURE.md`).

---

## 5. C4 · Ebene 3 — Components

Das Innere des Containers **MailShield App** = das Hexagon. Abhängigkeiten
zeigen nach innen.

```mermaid
flowchart LR
    subgraph driving["Driving-Adapter (eingehend)"]
        smtpA["SMTP Adapter<br/><i>mhale/smtpd</i>"]
        tgPoll["TG Update Poller<br/><i>Long-Polling</i>"]
    end

    subgraph core["KERN (core)"]
        direction TB
        ingest["Ingest UseCase<br/>(MailIngestor)"]
        reply["Reply UseCase<br/>(ReplyService)"]
        mime["MIME Parser<br/><i>enmime</i>"]
        analyzer["Security Analyzer<br/>(Verdicter: SPF+DKIM)"]
        router["Router<br/>(nach RCPT TO)"]
        ingest --> mime --> analyzer --> router
    end

    subgraph driven["Driven-Adapter (ausgehend)"]
        tgNotify["TG Notifier<br/>(Notifier)"]
        mailer["Mailer + DKIM Signer<br/>(MailSender/MessageSigner)"]
        store["SQLite Store<br/>(ConversationStore + UserRegistry)"]
        dnsA["DNS Resolver"]
    end

    smtpA --> ingest
    tgPoll --> reply
    router --> store
    router --> tgNotify
    analyzer --> dnsA
    reply --> store
    reply --> mailer

    style core fill:#1f6feb22,stroke:#1f6feb
```

**Komponenten und ihre Ports:**

| Komponente | Klasse | Port (Interface) |
| :--- | :--- | :--- |
| SMTP Adapter | driving | ruft `MailIngestor` auf |
| TG Update Poller | driving | ruft `ReplyService` auf |
| Ingest / Reply UseCase | Kern | implementieren Driving-Ports, orchestrieren die Domäne |
| Security Analyzer | Kern | `Verdicter` (ruft `DNSResolver` auf) |
| Router | Kern | nutzt `UserRegistry` + `ConversationStore` |
| TG Notifier | driven | `Notifier` |
| Mailer + Signer | driven | `MailSender` + `MessageSigner` |
| SQLite Store | driven | `ConversationStore` + `UserRegistry` |
| DNS Resolver | driven | `DNSResolver` |

> **Ebene 4 (Code)** wird nicht als eigenes Diagramm gezeichnet — die
> Port-Signaturen sind in §5 `ARCHITECTURE.md` (`ports.go`) festgehalten.

---

## 6. DFD · Datenflussdiagramme

**Legende:** `👤 externe Entität` · `([Prozess])` · `[(Datenspeicher)]`.

### 6.1. DFD Level 0 (Kontext)

```mermaid
flowchart LR
    sender["👤 Absender"]
    emp["👤 Mitarbeiter"]
    tg["👤 Telegram"]
    relay["👤 Relay/MX"]

    P0(["0 · MailShield"])

    sender -- "E-Mail (raw)" --> P0
    P0 -- "Benachrichtigung+Verdict" --> tg
    tg -- "Antworttext" --> P0
    emp -. "liest/schreibt" .- tg
    P0 -- "ausgehende E-Mail" --> relay
```

### 6.2. DFD Level 1 (Prozesszerlegung)

```mermaid
flowchart TB
    sender["👤 Absender"]
    tg["👤 Telegram"]
    dns["👤 DNS"]
    relay["👤 Relay/MX"]

    P1(["1 · Empfang und Parsing<br/>(SMTP + MIME)"])
    P2(["2 · Analyse<br/>(SPF/DKIM → Verdict)"])
    P3(["3 · Routing<br/>und Benachrichtigung"])
    P4(["4 · Antwortverarbeitung<br/>(Autorisierung)"])
    P5(["5 · Zusammenstellung und Versand<br/>(DKIM + Relay)"])

    D1[("D1 · users")]
    D2[("D2 · conversations")]
    D3[("D3 · messages/verdicts")]

    sender -- "raw email" --> P1
    P1 -- "ParsedEmail" --> P2
    P2 -- "TXT-Anfrage" --> dns
    dns -- "SPF/DKIM-Einträge" --> P2
    P2 -- "ParsedEmail+Verdict" --> P3
    P3 -- "ByEmail(rcpt)" --> D1
    P3 -- "Link(thread)" --> D2
    P3 -- "Verdict speichern" --> D3
    P3 -- "Notification" --> tg

    tg -- "reply (chat_id,thread_id)" --> P4
    P4 -- "Authorize" --> D1
    P4 -- "Resolve(thread)" --> D2
    P4 -- "ReplyCommand" --> P5
    P5 -- "Out-Msg speichern" --> D3
    P5 -- "signierte E-Mail" --> relay
```

---

## 7. Sequenzdiagramme

### 7.1. Eingehend: E-Mail → Telegram

```mermaid
sequenceDiagram
    autonumber
    participant Ext as Absender
    participant SMTP as SMTP Adapter
    participant UC as Ingest UseCase
    participant AN as Analyzer(Verdicter)
    participant DNS as DNS
    participant REG as UserRegistry (SQLite)
    participant CS as ConversationStore (SQLite)
    participant TG as TG Notifier

    Ext->>SMTP: MAIL FROM / RCPT TO=boris@ / DATA
    SMTP->>UC: Ingest(RawEmail)
    UC->>UC: parse MIME → ParsedEmail
    UC->>AN: Analyze(ParsedEmail)
    AN->>DNS: LookupTXT(Domain)
    DNS-->>AN: SPF/DKIM
    AN-->>UC: Verdict(spf,dkim,risk,label)
    UC->>REG: ByEmail("boris@shk.solutions")
    REG-->>UC: User{Boris, tg_chat_id}
    UC->>CS: Link(ConversationID, EmailThread)
    UC->>TG: Notify(Notification)
    TG->>TG: createForumTopic(chat_id) → thread_id
    TG->>TG: sendMessage(chat_id, thread_id, Text+Badge)
    SMTP-->>Ext: 250 OK
```

### 7.2. Ausgehend: Telegram → E-Mail

```mermaid
sequenceDiagram
    autonumber
    participant Boris as Boris (TG)
    participant POLL as TG Poller
    participant UC as Reply UseCase
    participant REG as UserRegistry (SQLite)
    participant CS as ConversationStore (SQLite)
    participant SIGN as DKIM Signer
    participant MAIL as Mailer
    participant Ext as Empfänger

    Boris->>POLL: Antwort im Topic
    POLL->>POLL: (chat_id,thread_id) → (UserID,ConversationID)
    POLL->>UC: SubmitReply(ReplyCommand)
    UC->>REG: Authorize(Boris, from="boris@")
    REG-->>UC: ok
    UC->>CS: Resolve(ConversationID)
    CS-->>UC: EmailThread{Message-ID,References,To}
    UC->>UC: OutgoingMessage zusammenstellen (From=boris@, Re:, In-Reply-To)
    UC->>SIGN: Sign(msg)  // d=shk.solutions
    UC->>MAIL: Send(msg)
    MAIL->>Ext: SMTP-Zustellung (Relay)
```

### 7.3. Ablehnung einer unbekannten Adresse

```mermaid
sequenceDiagram
    autonumber
    participant Ext as Absender
    participant SMTP as SMTP Adapter
    participant REG as UserRegistry (SQLite)
    Ext->>SMTP: RCPT TO=random@shk.solutions
    SMTP->>REG: ByEmail("random@shk.solutions")
    REG-->>SMTP: not found
    SMTP-->>Ext: 550 unknown user
```

---

## 8. Datenmodell (SQLite)

```sql
-- Mitarbeiter / Postfächer (Routing-Register + Autorisierung)
CREATE TABLE users (
    id           INTEGER PRIMARY KEY,
    email        TEXT    NOT NULL UNIQUE,        -- boris@shk.solutions
    display_name TEXT,
    tg_chat_id   INTEGER NOT NULL,               -- persönliche Gruppe (-100...)
    active       INTEGER NOT NULL DEFAULT 1
);

-- Konversationen (Thread = externer Kontakt + Owner + Topic)
CREATE TABLE conversations (
    id              TEXT    PRIMARY KEY,          -- ConversationID (uuid)
    owner_user_id   INTEGER NOT NULL REFERENCES users(id),
    external_addr   TEXT    NOT NULL,             -- client@acme.com
    subject         TEXT,
    root_message_id TEXT,                         -- Message-ID der ersten E-Mail
    tg_thread_id    INTEGER,                      -- message_thread_id des Topics
    created_at      TEXT    NOT NULL,
    updated_at      TEXT    NOT NULL,
    UNIQUE(owner_user_id, external_addr)          -- ein Thread pro Kontakt und Mitarbeiter
);

-- Nachrichten (Threading + Verlauf)
CREATE TABLE messages (
    id              INTEGER PRIMARY KEY,
    conversation_id TEXT    NOT NULL REFERENCES conversations(id),
    direction       TEXT    NOT NULL,             -- 'in' | 'out'
    message_id      TEXT,                         -- Message-ID
    in_reply_to     TEXT,
    refs            TEXT,                         -- References
    from_addr       TEXT,
    to_addr         TEXT,
    subject         TEXT,
    body_preview    TEXT,
    created_at      TEXT    NOT NULL
);

-- Analyse-Verdicts
CREATE TABLE verdicts (
    message_pk INTEGER PRIMARY KEY REFERENCES messages(id),
    spf        TEXT,        -- pass/fail/softfail/none
    dkim       TEXT,        -- pass/fail/none
    risk       INTEGER,     -- 1..10
    label      TEXT,        -- clean/suspicious/malicious
    details    TEXT         -- JSON (rohe Details)
);

-- Öffnen der DB: PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;
```

**Zuordnung zu Ports:** `users` → `UserRegistry`;
`conversations` + `messages` → `ConversationStore`; `verdicts` → Analyse-Verlauf.

---

## 9. Externe Schnittstellen und Verträge

| Interface | Richtung | Vertrag (MVP) |
| :--- | :--- | :--- |
| **SMTP-Empfang** | eingehend | `MAIL FROM`, `RCPT TO`, `DATA`; `250` bei Annahme, `550` für unbekannte Adresse, `4xx` bei Warteschlangen-Überlauf |
| **Telegram Bot API** | ein-/ausgehend | `getUpdates` (Long-Poll); `createForumTopic`, `sendMessage` (parse_mode, `message_thread_id`) |
| **DNS** | ausgehend | `LookupTXT` für SPF; öffentlicher DKIM-Schlüssel `selector._domainkey.<domain>` |
| **SMTP-Relay** | ausgehend | AUTH + STARTTLS zum Smart Host; E-Mail mit `DKIM-Signature`, `In-Reply-To`, `References` |

**Anforderungen an die Telegram-Einrichtung (MVP):** ein Bot bei `@BotFather`;
eine Supergroup pro Mitarbeiter mit aktivierten Topics; der Bot ist Admin mit
dem Recht `can_manage_topics`.

---

## 10. Konfiguration

Über Umgebungsvariablen (12-Factor); Secrets nicht im Image.

```
BIND_ADDR=0.0.0.0:2525          # SMTP-Listener
DB_PATH=/data/mailshield.db     # SQLite (Docker-Volume)
TELEGRAM_BOT_TOKEN=...          # ein Token für alles
DOMAIN=shk.solutions
DKIM_SELECTOR=mail
DKIM_KEY_PATH=/keys/dkim_private.pem
RELAY_ADDR=smtp.relay.example:587
RELAY_USER=...
RELAY_PASS=...
```

Das Benutzerregister liegt in der Tabelle `users` (Seeding aus der
Konfiguration beim Start):

```yaml
users:
  - email: fima@shk.solutions
    tg_chat_id: -1001111111111
  - email: boris@shk.solutions
    tg_chat_id: -1002222222222
```

---

## 11. Fertigstellungskriterien des MVP (Definition of Done)

- [ ] Eine E-Mail an `boris@shk.solutions` erscheint als **neues Topic** in
      Boris' Gruppe mit dem Badge `SPF/DKIM`.
- [ ] Die Antwort im Topic wird als E-Mail **`From: boris@`** zugestellt,
      korrekt **threaded** (`In-Reply-To`/`References`) und **DKIM-signiert**.
- [ ] Eine E-Mail an zwei Adressen (`fima@`, `boris@`) geht an **beide** in
      ihre jeweiligen Gruppen (Fan-out-Routing).
- [ ] Eine E-Mail an eine unbekannte Adresse → **`550`**.
- [ ] Von einer fremden Adresse aus einem „nicht eigenen" Chat zu antworten
      ist **nicht möglich** (Autorisierung).
- [ ] Ein **Container-Neustart** erhält das Mapping von Konversationen und
      Threads (SQLite).
- [ ] Der Kern ist mit Unit-Tests **auf Basis von Port-Mocks** abgedeckt (ohne
      Netzwerk/SMTP/Telegram).

---

## 12. Annahmen und Risiken

| # | Annahme / Risiko | Auswirkung auf das Design |
| :--- | :--- | :--- |
| A1 | **Ausgehender Port 25/587** ist offen (oder ein Relay ist erreichbar) | `telnet ... 25` prüfen. Wenn geschlossen und kein Relay erreichbar ist — der ausgehende Zyklus des MVP schlägt fehl; Blocker |
| A2 | **Deliverability**: PTR, SPF, DKIM, DMARC sind konfiguriert | Sonst landen Antworten im Spam; für das MVP nutzen wir **Relay über Smart Host** |
| A3 | Der Bot ist **Admin mit `can_manage_topics`** | Ohne dieses Recht kann kein Topic erstellt werden → Variante 2 funktioniert nicht |
| A4 | **Menschliches Tempo** beim E-Mail-Aufkommen | Rechtfertigt SQLite + einen Prozess; Skalierung wird nicht ausgelegt |
| A5 | Telegram-Nachrichtenlimit (4096) | Bei langen E-Mails wird die Vorschau gekürzt (vollständiger Body/Anhänge — außerhalb des MVP) |
| A6 | HTML-E-Mails | Im MVP wird `text` verwendet, bei Fehlen davon ein gekürztes `html` als Text |

---

*MVP-Design-Dokument vom 2026-07-24. Die vollständige Zielarchitektur, Ports
und das Entscheidungsprotokoll stehen in [`ARCHITECTURE.md`](./ARCHITECTURE.md).
Im Verlauf der Umsetzung aktualisieren Sie §8 (Schema), §11 (DoD) und §12
(Risiken).*
