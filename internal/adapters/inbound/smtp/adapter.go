package smtp

import (
	"context"
	"fmt"
	"log"
	"net"
	"strings"

	"github.com/mhale/smtpd"
	"mailshield/internal/core"
)

// Adapter wraps mhale/smtpd and translates SMTP events into core port calls.
type Adapter struct {
	ingestor core.MailIngestor
	registry core.UserRegistry
	server   *smtpd.Server
}

func New(addr, hostname string, ingestor core.MailIngestor, registry core.UserRegistry) *Adapter {
	a := &Adapter{ingestor: ingestor, registry: registry}
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
	log.Printf("[smtp] listening on %s", a.server.Addr)
	return a.server.ListenAndServe()
}

// handleRcpt runs at RCPT TO time — rejects unknown addresses with 550.
func (a *Adapter) handleRcpt(_ net.Addr, _ string, to string) bool {
	_, ok := a.registry.ByEmail(to)
	if !ok {
		log.Printf("[smtp] 550 unknown user: %s", to)
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
	if err := a.ingestor.Ingest(context.Background(), raw); err != nil {
		log.Printf("[smtp] ingest error from=%s: %v", from, err)
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
