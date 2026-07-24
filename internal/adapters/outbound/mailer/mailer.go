package mailer

import (
	"bytes"
	"context"
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"log"
	"net"
	"net/smtp"
	"os"
	"strings"
	"time"

	"github.com/emersion/go-msgauth/dkim"
	"mailshield/internal/core"
)

// Mailer implements core.MailSender using direct MTA delivery (MX lookup → SMTP)
// with optional DKIM signing.
type Mailer struct {
	domain   string
	selector string
	signer   crypto.Signer // nil if key not loaded
}

func New(domain, selector, keyPath string) *Mailer {
	m := &Mailer{domain: domain, selector: selector}
	if keyPath == "" {
		return m
	}
	key, err := loadPrivateKey(keyPath)
	if err != nil {
		log.Printf("[mailer] DKIM key not loaded (%s): %v — will send unsigned", keyPath, err)
		return m
	}
	m.signer = key
	log.Printf("[mailer] DKIM ready (domain=%s selector=%s)", domain, selector)
	return m
}

func (m *Mailer) Send(ctx context.Context, msg core.OutgoingMessage) error {
	raw := buildMIME(msg, m.domain)

	if m.signer != nil {
		signed, err := signDKIM(raw, m.signer, m.domain, m.selector)
		if err != nil {
			log.Printf("[mailer] DKIM sign error: %v — sending unsigned", err)
		} else {
			raw = signed
		}
	}

	parts := strings.Split(msg.To, "@")
	if len(parts) != 2 {
		return fmt.Errorf("invalid To address: %s", msg.To)
	}

	mxRecords, err := net.LookupMX(parts[1])
	if err != nil {
		return fmt.Errorf("MX lookup for %s: %w", parts[1], err)
	}
	if len(mxRecords) == 0 {
		return fmt.Errorf("no MX records for %s", parts[1])
	}

	mx := strings.TrimSuffix(mxRecords[0].Host, ".")
	addr := mx + ":25"

	log.Printf("[mailer] delivering from=%s to=%s via %s", msg.From, msg.To, addr)
	if err := deliver(addr, m.domain, msg.From, msg.To, raw); err != nil {
		return fmt.Errorf("deliver to %s: %w", addr, err)
	}
	log.Printf("[mailer] delivered to=%s subject=%q", msg.To, msg.Subject)
	return nil
}

func deliver(addr, helo, from, to string, body []byte) error {
	c, err := smtp.Dial(addr)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer c.Close()

	if err = c.Hello(helo); err != nil {
		return fmt.Errorf("EHLO: %w", err)
	}

	if ok, _ := c.Extension("STARTTLS"); ok {
		host := strings.Split(addr, ":")[0]
		if err = c.StartTLS(&tls.Config{ServerName: host}); err != nil {
			return fmt.Errorf("STARTTLS: %w", err)
		}
	}

	if err = c.Mail(from); err != nil {
		return fmt.Errorf("MAIL FROM: %w", err)
	}
	if err = c.Rcpt(to); err != nil {
		return fmt.Errorf("RCPT TO: %w", err)
	}

	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("DATA: %w", err)
	}
	if _, err = w.Write(body); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	if err = w.Close(); err != nil {
		return fmt.Errorf("end DATA: %w", err)
	}
	return c.Quit()
}

func buildMIME(msg core.OutgoingMessage, domain string) []byte {
	var buf bytes.Buffer
	msgID := fmt.Sprintf("<%d@%s>", time.Now().UnixNano(), domain)

	headers := [][2]string{
		{"From", msg.From},
		{"To", msg.To},
		{"Subject", msg.Subject},
		{"Message-ID", msgID},
		{"Date", time.Now().UTC().Format("Mon, 02 Jan 2006 15:04:05 -0700")},
		{"MIME-Version", "1.0"},
		{"Content-Type", "text/plain; charset=utf-8"},
	}
	if msg.InReplyTo != "" {
		headers = append(headers, [2]string{"In-Reply-To", msg.InReplyTo})
	}
	if len(msg.References) > 0 {
		headers = append(headers, [2]string{"References", strings.Join(msg.References, " ")})
	}

	for _, h := range headers {
		fmt.Fprintf(&buf, "%s: %s\r\n", h[0], h[1])
	}
	buf.WriteString("\r\n")
	buf.WriteString(msg.Body)
	buf.WriteString("\r\n")

	return buf.Bytes()
}

func signDKIM(raw []byte, key crypto.Signer, domain, selector string) ([]byte, error) {
	opts := &dkim.SignOptions{
		Domain:   domain,
		Selector: selector,
		Signer:   key,
	}
	var out bytes.Buffer
	if err := dkim.Sign(&out, bytes.NewReader(raw), opts); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func loadPrivateKey(path string) (crypto.Signer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("no PEM block in %s", path)
	}

	// try PKCS8 (openssl genpkey)
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if s, ok := key.(crypto.Signer); ok {
			return s, nil
		}
	}
	// try PKCS1 (openssl genrsa)
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	// try EC
	if key, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	return nil, fmt.Errorf("unsupported key format in %s", path)
}
