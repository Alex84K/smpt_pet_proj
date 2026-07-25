package smtp

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"

	"github.com/mhale/smtpd"
	"mailshield/internal/core"
)

// Adapter wraps mhale/smtpd and translates SMTP events into core port calls.
type Adapter struct {
	ingestor core.MailIngestor
	registry core.UserRegistry
	aliases  map[string]bool
	server   *smtpd.Server
}

func New(addr, hostname string, ingestor core.MailIngestor, registry core.UserRegistry, aliases []string) *Adapter {
	a := &Adapter{
		ingestor: ingestor,
		registry: registry,
		aliases:  make(map[string]bool, len(aliases)),
	}
	for _, al := range aliases {
		a.aliases[strings.ToLower(al)] = true
	}
	a.server = &smtpd.Server{
		Addr:        addr,
		Handler:     a.handleMail,
		HandlerRcpt: a.handleRcpt,
		Appname:     "MailShield",
		Hostname:    hostname,
	}
	return a
}

// ListenAndServe blocks until the server stops.
func (a *Adapter) ListenAndServe() error {
	slog.Info("smtp listening", "addr", a.server.Addr)
	return a.server.ListenAndServe()
}

// handleRcpt runs at RCPT TO time — rejects unknown addresses with 550.
func (a *Adapter) handleRcpt(_ net.Addr, _ string, to string) bool {
	to = strings.ToLower(to)
	if a.aliases[to] {
		return true
	}
	_, ok := a.registry.ByEmail(to)
	if !ok {
		slog.Info("smtp reject unknown", "to", to)
	}
	return ok
}

// handleMail runs after DATA — all recipients already validated by handleRcpt.
func (a *Adapter) handleMail(origin net.Addr, from string, to []string, data []byte) error {
	ip := senderIP(origin)
	raw := core.RawEmail{
		SenderIP: ip,
		From:     from,
		To:       to,
		Data:     data,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := a.ingestor.Ingest(ctx, raw); err != nil {
		slog.Error("ingest failed", "from", from, "err", err)
		return fmt.Errorf("temporary server error")
	}
	return nil
}

func senderIP(addr net.Addr) string {
	if addr == nil {
		return "127.0.0.1"
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return strings.Split(addr.String(), ":")[0]
	}
	return host
}
