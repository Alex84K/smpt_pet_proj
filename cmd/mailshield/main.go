package main

import (
	"context"
	"log"
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
	// --- config ---
	tgToken      := mustEnv("TG_TOKEN")
	bindAddr     := envOr("BIND_ADDR", "0.0.0.0:2525")
	hostname     := envOr("HOSTNAME", "shk.solutions")
	dbPath       := envOr("DB_PATH", "mailshield.db")
	dkimKeyPath  := envOr("DKIM_KEY_PATH", "keys/dkim_private.pem")
	dkimSelector := envOr("DKIM_SELECTOR", "mail")

	// --- SQLite store (ConversationStore + UserRegistry + TopicIndex + AdminStore) ---
	// Starts empty; the admin provisions mailboxes at runtime via Telegram commands.
	db, err := sqlite.New(dbPath)
	if err != nil {
		log.Fatalf("[main] sqlite: %v", err)
	}

	// --- telegram client ---
	tgClient, err := telegram.NewClient(tgToken, db) // db implements MessageIndex
	if err != nil {
		log.Fatalf("[main] telegram: %v", err)
	}
	tgNotif := telegram.NewNotifier(tgClient)

	// --- aliases (fan-out to all active users) ---
	aliases := []string{"team@shk.solutions"}

	// --- driven adapters ---
	verd       := dnsadapter.New()
	mailSender := mailer.New(hostname, dkimSelector, dkimKeyPath)

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
			log.Printf("[main] smtp stopped: %v", err)
		}
	}()
	go tgPoller.Run(ctx)

	log.Println("[MailShield] Etap 6 — admin panel via Telegram live")
	log.Printf("[MailShield] bind=%s domain=%s db=%s admin=%d", bindAddr, hostname, dbPath, adminID)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	log.Println("[MailShield] shutting down...")
	cancel()
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("[main] required env var %s is not set", key)
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

