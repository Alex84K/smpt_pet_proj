# MailShield

Email ↔ Telegram bridge for small teams. Receives inbound mail, routes it to a personal Telegram supergroup as forum topics (one topic per external contact), and delivers replies back as real DKIM-signed email — no mail client required.

**Live:** `support@shk.solutions` · VPS `82.165.47.33` · Go + Docker

---

## How it works

```
Internet ──SMTP──▶ MailShield ──▶ SPF check ──▶ Telegram Forum Topic
                                                        │
                                                 user replies in topic
                                                        │
                                                        ▼
Internet ◀──SMTP── direct MTA ◀── DKIM sign ◀── ReplyService
```

1. Inbound email arrives at port 25 (MX → VPS)
2. SPF is checked, risk scored 1–10
3. If the sender is new → a forum **Topic** is created in the user's supergroup
4. Email is posted as a formatted HTML message in that topic
5. User replies inside the topic → MailShield sends it as a real email to the original sender

---

## Architecture

Hexagonal (ports & adapters). The core domain has no knowledge of SMTP framing, Telegram chat IDs, or SQL — only interfaces.

```
cmd/mailshield/         — composition root
internal/
  core/
    model.go            — domain types (User, ParsedEmail, ConversationID…)
    ports.go            — all interfaces (driving + driven)
    app/
      ingest.go         — MailIngestor use-case
      reply.go          — ReplyService use-case
  adapters/
    inbound/smtp/       — SMTP server (mhale/smtpd)
    outbound/dns/       — SPF verdicter via net.LookupTXT
    outbound/mailer/    — direct MTA delivery + DKIM (emersion/go-msgauth)
    sqlite/             — ConversationStore + UserRegistry + TopicIndex
    telegram/           — forum-topic notifier + update poller
    fake/, inmem/       — test doubles
keys/                   — DKIM PEM files (gitignored)
```

**Driving ports:** `MailIngestor`, `ReplyService`  
**Driven ports:** `Notifier`, `MailSender`, `Verdicter`, `ConversationStore`, `UserRegistry`, `TopicIndex`

---

## Prerequisites

- Go 1.21+
- VPS with **port 25 open** (blocked on most residential ISPs)
- Domain DNS:

| Record | Value |
|--------|-------|
| `MX` | `shk.solutions.` → VPS IP |
| `TXT` (SPF) | `v=spf1 ip4:<VPS_IP> -all` |
| `TXT` (DKIM) | `mail._domainkey` → public key |
| `PTR` | VPS IP → `shk.solutions` |

---

## Environment variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `TG_TOKEN` | ✓ | — | Telegram Bot token |
| `BIND_ADDR` | | `0.0.0.0:2525` | SMTP listen address inside container |
| `HOSTNAME` | | `shk.solutions` | SMTP hostname / MAIL FROM domain |
| `DB_PATH` | | `mailshield.db` | SQLite database path |
| `DKIM_KEY_PATH` | | `keys/dkim_private.pem` | DKIM RSA private key (PEM) |
| `DKIM_SELECTOR` | | `mail` | DKIM selector |

Only `TG_TOKEN` is secret and belongs in `.env`. Everything else is in `docker-compose.yml`.

---

## Local run

```bash
# 1. Install dependencies
go mod download

# 2. Create .env with your bot token
echo "TG_TOKEN=<your_token>" > .env

# 3. Run
export $(cat .env | xargs)
go run ./cmd/mailshield

# 4. Send a test email (requires Bun)
HOST=localhost PORT=2525 bun bun/send.ts

# 5. Tests
go test ./...
```

---

## Deploy to VPS

**Directory layout on the server:**

```
/srv/mailshield/
├── docker-compose.yml
├── Dockerfile
├── .env                  ← TG_TOKEN only — create manually
├── keys/
│   └── dkim_private.pem  ← already on VPS from deliverability setup
└── data/                 ← created by Docker; holds mailshield.db
```

**Sync and start:**

```bash
# From local machine
rsync -av --exclude='.env' --exclude='data/' \
  ./go_mail_serv/ root@82.165.47.33:/srv/mailshield/

# On VPS
ssh root@82.165.47.33
cd /srv/mailshield
echo "TG_TOKEN=<token>" > .env   # only if not already there
docker compose up -d --build
docker compose logs -f
```

---

## Telegram supergroup setup (Forum Topics)

Each user needs their own **supergroup with Topics enabled**. The bot creates one topic per external contact automatically.

**Setup steps:**

1. Create a Telegram supergroup
2. Enable topics: **Group Settings → Topics ✓**
3. Add `@your_bot` as admin → grant **Delete messages** and **Manage topics**
4. Get the group's `chat_id` — forward any group message to `@userinfobot`
5. Update the DB on VPS:

```bash
sqlite3 ./data/mailshield.db \
  "UPDATE users SET tg_chat_id=-1001234567890 WHERE email='boris@shk.solutions';"
```

No restart needed — the poller reads the DB per-request.

**Adding a new user:**

```bash
# 1. User messages the bot → bot replies with their chat_id
# 2. Update DB:
sqlite3 ./data/mailshield.db \
  "UPDATE users SET tg_chat_id=<CHAT_ID> WHERE email='fima@shk.solutions';"
```

---

## Users and aliases

Users are seeded from `cmd/mailshield/main.go` on first run (`INSERT OR IGNORE`). After that, the **database is the source of truth** — restart does not overwrite existing records.

| Address | Type | Description |
|---------|------|-------------|
| `boris@shk.solutions` | user | ID 1, seeded with chat_id |
| `fima@shk.solutions` | user | ID 2, chat_id set via DB |
| `team@shk.solutions` | alias | fan-out → all active users |

---

## Roadmap

| # | Description | Status |
|---|-------------|--------|
| 0 | Deliverability spike — PTR, SPF, DKIM on VPS | ✅ done |
| 1 | SMTP inbound + Telegram notification | ✅ done |
| 2 | Reply from Telegram → outbound email | ✅ done |
| 3 | SQLite persistence | ✅ done |
| 4 | Multi-user routing + `team@` alias fan-out | ✅ done |
| 5 | Telegram Forum Topics (one topic per external contact) | ✅ done |
| 6 | Hardening — slog JSON, context timeouts, golangci-lint | ⬜ next |

---

## Key dependencies

| Package | Role |
|---------|------|
| `mhale/smtpd` | SMTP server |
| `jhillyerd/enmime` | MIME parsing |
| `go-telegram-bot-api/v5` | Telegram Bot API |
| `emersion/go-msgauth` | DKIM signing |
| `modernc.org/sqlite` | Pure-Go SQLite (CGO_ENABLED=0) |
