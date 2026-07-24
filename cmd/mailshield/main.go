package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"mailshield/internal/adapters/fake"
	smtpadapter "mailshield/internal/adapters/inbound/smtp"
	dnsadapter "mailshield/internal/adapters/outbound/dns"
	"mailshield/internal/adapters/outbound/mailer"
	"mailshield/internal/adapters/sqlite"
	"mailshield/internal/adapters/telegram"
	"mailshield/internal/core"
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

	// --- SQLite store (ConversationStore + UserRegistry + TG MessageIndex) ---
	db, err := sqlite.New(dbPath)
	if err != nil {
		log.Fatalf("[main] sqlite: %v", err)
	}

	// seed users on first run only (INSERT OR IGNORE — DB is the source of truth after that)
	seedUsers(db, []core.User{
		{ID: 1, Email: "boris@shk.solutions", DisplayName: "Boris", TGChatID: 5238002828},
		{ID: 2, Email: "fima@shk.solutions", DisplayName: "Fima", TGChatID: 0},
	})

	// --- telegram client ---
	tgClient, err := telegram.NewClient(tgToken, db) // db implements MessageIndex
	if err != nil {
		log.Fatalf("[main] telegram: %v", err)
	}
	tgNotif := telegram.NewNotifier(tgClient)

	// --- driven adapters ---
	verd       := dnsadapter.New()
	mailSender := mailer.New(hostname, dkimSelector, dkimKeyPath)

	// --- use-cases ---
	ingest := app.NewIngestUseCase(verd, db, db, tgNotif)  // db as UserRegistry + ConversationStore
	reply  := app.NewReplyUseCase(db, db, fake.NewSigner(), mailSender, hostname)

	// --- driving adapters ---
	smtpSrv  := smtpadapter.New(bindAddr, hostname, ingest, db)
	tgPoller := telegram.NewPoller(tgClient, db, reply)

	// --- run ---
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		if err := smtpSrv.ListenAndServe(); err != nil {
			log.Printf("[main] smtp stopped: %v", err)
		}
	}()
	go tgPoller.Run(ctx)

	log.Println("[MailShield] Etap 3 — SQLite persistence live")
	log.Printf("[MailShield] bind=%s domain=%s db=%s", bindAddr, hostname, dbPath)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	log.Println("[MailShield] shutting down...")
	cancel()
}

func seedUsers(db *sqlite.Store, users []core.User) {
	for _, u := range users {
		if err := db.AddUser(u); err != nil {
			log.Printf("[main] seed user %s: %v", u.Email, err)
		}
	}
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

