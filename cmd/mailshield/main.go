package main

import (
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"mailshield/internal/adapters/fake"
	"mailshield/internal/adapters/inmem"
	smtpadapter "mailshield/internal/adapters/inbound/smtp"
	dnsadapter "mailshield/internal/adapters/outbound/dns"
	tgadapter "mailshield/internal/adapters/outbound/telegram"
	"mailshield/internal/core"
	"mailshield/internal/core/app"
)

func main() {
	// --- config from env ---
	tgToken  := mustEnv("TG_TOKEN")
	bindAddr := envOr("BIND_ADDR", "0.0.0.0:2525")
	hostname  := envOr("HOSTNAME", "shk.solutions")

	// --- registry: seed users (config-driven from Etap 4) ---
	reg := inmem.NewUserRegistry()
	reg.Add(core.User{
		ID:          1,
		Email:       "boris@shk.solutions",
		DisplayName: "Boris",
		TGChatID:    parseChatID(envOr("TG_CHAT_BORIS", "0")),
	})
	reg.Add(core.User{
		ID:          2,
		Email:       "fima@shk.solutions",
		DisplayName: "Fima",
		TGChatID:    parseChatID(envOr("TG_CHAT_FIMA", "0")),
	})

	// --- driven adapters ---
	store := inmem.NewStore()

	tgNotif, err := tgadapter.NewNotifier(tgToken)
	if err != nil {
		log.Fatalf("[main] telegram init: %v", err)
	}

	verd   := dnsadapter.New()
	signer := fake.NewSigner()  // real DKIM signer: Etap 2
	sender := fake.NewMailSender() // real mailer: Etap 2

	// --- use-cases ---
	ingest := app.NewIngestUseCase(verd, reg, store, tgNotif)
	reply  := app.NewReplyUseCase(reg, store, signer, sender, hostname)
	_ = reply // wired to TG poller in Etap 2

	// --- driving adapters ---
	smtpSrv := smtpadapter.New(bindAddr, hostname, ingest, reg)

	go func() {
		if err := smtpSrv.ListenAndServe(); err != nil {
			log.Printf("[main] smtp stopped: %v", err)
		}
	}()

	log.Println("[MailShield] Etap 1 — SMTP + Telegram notifier live")
	log.Printf("[MailShield] listening on %s | domain=%s", bindAddr, hostname)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	log.Println("[MailShield] shutdown")
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
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return id
}
