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
    sqlite/             — ConversationStore + UserRegistry + TopicIndex + AdminStore
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
| `TG_ADMIN_ID` | ✓ | — | Telegram `user_id` of the admin who provisions mailboxes |
| `BIND_ADDR` | | `0.0.0.0:2525` | SMTP listen address inside container |
| `HOSTNAME` | | `shk.solutions` | SMTP hostname / MAIL FROM domain |
| `DB_PATH` | | `mailshield.db` | SQLite database path |
| `DKIM_KEY_PATH` | | `keys/dkim_private.pem` | DKIM RSA private key (PEM) |
| `DKIM_SELECTOR` | | `mail` | DKIM selector |

`TG_TOKEN` and `TG_ADMIN_ID` live in `.env`; everything else is in `docker-compose.yml`. To find your `TG_ADMIN_ID`, message the bot — it replies with your `chat_id`.

---

## Local run

```bash
# 1. Install dependencies
go mod download

# 2. Create .env with your bot token and admin user_id
printf 'TG_TOKEN=<your_token>\nTG_ADMIN_ID=<your_user_id>\n' > .env

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
├── .env                  ← TG_TOKEN + TG_ADMIN_ID — create manually
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
printf 'TG_TOKEN=<token>\nTG_ADMIN_ID=<user_id>\n' > .env   # only if not already there
docker compose up -d --build
docker compose logs -f
```

**Starting with an empty database** (one-time reset — the seed users are gone; all
mailboxes are now created at runtime via Telegram):

```bash
cd /srv/mailshield
docker compose down
rm -f ./data/mailshield.db*          # .db, .db-wal, .db-shm
docker compose up -d --build
```

---

## Admin panel (Telegram)

All mailbox management happens through the bot in a **direct chat with the admin**
(the account whose `user_id` matches `TG_ADMIN_ID`). No shell, no SQL.

| Command | Action |
|---------|--------|
| `/adduser email [name]` | Create a mailbox → bot returns a **bind code** |
| `/bind CODE` | *(run inside the target supergroup)* link that mailbox to this group |
| `/users` | List mailboxes and their bind status |
| `/setchat email chat_id` | Manually set a mailbox's chat_id (fallback) |
| `/deluser email` | Remove a mailbox |
| `/help` | Show the command list |

Bind codes are single-use, generated with `crypto/rand`, and stored in the
`bind_codes` table until consumed.

---

## Onboarding a user (Forum Topics)

Each user gets their own **supergroup with Topics enabled**. The bot creates one
topic per external contact automatically. A user's stored chat_id **must** be the
supergroup id (negative, `-100…`) — that's why binding happens inside the group.

**Steps:**

1. Create a Telegram supergroup and enable **Group Settings → Topics ✓**
2. Add `@your_bot` as admin → grant **Delete messages** and **Manage topics**
3. Admin (in DM with the bot): `/adduser fima@shk.solutions Fima` → bot replies with a code
4. Anyone in the supergroup: `/bind <code>` → the bot captures the group id and links it

```
Admin (DM with bot):
  /adduser fima@shk.solutions Fima
  → ✅ Mailbox created. Bind code: A1B2C3
     Send /bind A1B2C3 in Fima's supergroup.

In Fima's supergroup:
  /bind A1B2C3
  → ✅ fima@shk.solutions linked to this group
```

No restart needed — the poller reads the DB per-request. The `/bind` step also works
as a manual fallback via `/setchat email <chat_id>` if you already know the group id
(the number after `#` in the `web.telegram.org` URL).

---

## Users and aliases

The database **starts empty**. Mailboxes are created at runtime by the admin via
`/adduser`; after that, the database is the sole source of truth. The admin's
authority comes from `TG_ADMIN_ID` (env), independent of the `users` table — so the
admin can bootstrap everything on a fresh database. Note the admin still needs their
own `/adduser` to *receive* mail.

| Address | Type | Description |
|---------|------|-------------|
| `boris@shk.solutions` | user | created via `/adduser`, bound to a supergroup |
| `fima@shk.solutions` | user | created via `/adduser`, bound to a supergroup |
| `team@shk.solutions` | alias | fan-out → all active users (configured in `main.go`) |

---

## Observability & hardening (Etap 7)

### Structured logging

All log output is JSON (`log/slog` with `JSONHandler`). Each line is a machine-parseable
object — easy to `grep` with `jq` or forward to a log aggregator:

```bash
# on VPS — filter by level
docker compose logs -f | jq 'select(.level=="ERROR")'

# watch delivery attempts
docker compose logs -f | jq 'select(.msg=="delivering")'
```

Typical startup output:

```json
{"time":"...","level":"INFO","msg":"sqlite ready","path":"/app/data/mailshield.db"}
{"time":"...","level":"INFO","msg":"telegram authorised","username":"YourBot"}
{"time":"...","level":"INFO","msg":"smtp listening","addr":"0.0.0.0:2525"}
{"time":"...","level":"INFO","msg":"MailShield started","bind":"0.0.0.0:2525","domain":"shk.solutions","admin":5238002828}
```

### Network timeouts

Every blocking network call now has an explicit deadline — a hung MX server or slow
Telegram API can no longer freeze the process:

| Call | Timeout |
|------|---------|
| DNS `LookupTXT` (SPF check) | 10 s |
| DNS `LookupMX` (outbound delivery) | 10 s |
| TCP dial to remote MTA | 15 s |
| Full ingest pipeline per email | 30 s |

### Panic recovery

The Telegram update poller wraps each incoming message in a `defer recover()`. A
malformed update that causes a panic is logged as `ERROR` and skipped — the poller
continues processing subsequent messages.

### golangci-lint in CI

`.golangci.yml` enables `errcheck`, `govet`, `staticcheck`, `ineffassign`, `misspell`,
`gosec`, `bodyclose`, and `noctx`. The lint job runs on every push/PR before the Docker
image is built.

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
| 6 | Admin panel via Telegram (`/adduser`, bind codes, empty-DB bootstrap) | ✅ done |
| 7 | Hardening — slog JSON, context timeouts, panic recovery, golangci-lint CI | ✅ done |

---

## Key dependencies

| Package | Role |
|---------|------|
| `mhale/smtpd` | SMTP server |
| `jhillyerd/enmime` | MIME parsing |
| `go-telegram-bot-api/v5` | Telegram Bot API |
| `emersion/go-msgauth` | DKIM signing |
| `modernc.org/sqlite` | Pure-Go SQLite (CGO_ENABLED=0) |
