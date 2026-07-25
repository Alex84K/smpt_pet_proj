package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"mailshield/internal/adapters/fake"
	smtpadapter "mailshield/internal/adapters/inbound/smtp"
	dnsadapter "mailshield/internal/adapters/outbound/dns"
	"mailshield/internal/adapters/outbound/mailer"
	"mailshield/internal/adapters/sqlite"
	"mailshield/internal/adapters/telegram"
	"mailshield/internal/core/app"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	// --- config ---
	tgToken      := mustEnv("TG_TOKEN")
	bindAddr     := envOr("BIND_ADDR", "0.0.0.0:2525")
	hostname     := envOr("HOSTNAME", "shk.solutions")
	dbPath       := envOr("DB_PATH", "mailshield.db")
	dkimKeyPath  := envOr("DKIM_KEY_PATH", "keys/dkim_private.pem")
	dkimSelector := envOr("DKIM_SELECTOR", "mail")

	// --- SQLite store (ConversationStore + UserRegistry + TopicIndex + AdminStore) ---
	db, err := sqlite.New(dbPath)
	if err != nil {
		slog.Error("sqlite init failed", "err", err)
		os.Exit(1)
	}

	// --- telegram client ---
	tgClient, err := telegram.NewClient(tgToken, db)
	if err != nil {
		slog.Error("telegram init failed", "err", err)
		os.Exit(1)
	}
	tgNotif := telegram.NewNotifier(tgClient)

	// --- aliases (fan-out to all active users) ---
	aliases := []string{"team@shk.solutions"}

	// --- driven adapters ---
	verd       := dnsadapter.New()
	mailSender := mailer.New(
		hostname, dkimSelector, dkimKeyPath,
		envOr("SMTP_RELAY_HOST", ""),
		envOr("SMTP_RELAY_PORT", "587"),
		envOr("SMTP_RELAY_USER", ""),
		envOr("SMTP_RELAY_PASS", ""),
	)

	// --- use-cases ---
	ingest := app.NewIngestUseCase(verd, db, db, tgNotif, aliases...)
	reply  := app.NewReplyUseCase(db, db, fake.NewSigner(), mailSender, hostname)

	// --- driving adapters ---
	adminID  := parseChatID(mustEnv("TG_ADMIN_ID"))
	smtpSrv  := smtpadapter.New(bindAddr, hostname, ingest, db, aliases)
	tgPoller := telegram.NewPoller(tgClient, db, reply, adminID, db)

	// --- run ---
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		if err := smtpSrv.ListenAndServe(); err != nil {
			slog.Error("smtp stopped", "err", err)
		}
	}()
	go tgPoller.Run(ctx)

	slog.Info("MailShield started", "bind", bindAddr, "domain", hostname, "db", dbPath, "admin", adminID)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	slog.Info("shutting down")
	cancel()
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		slog.Error("required env var not set", "key", key)
		os.Exit(1)
	}
	return v
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func parseChatID(s string) int64 {
	id, _ := strconv.ParseInt(s, 10, 64)
	return id
}

