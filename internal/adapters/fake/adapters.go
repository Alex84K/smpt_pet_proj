package fake

import (
	"context"
	"log"

	"mailshield/internal/core"
)

// Notifier logs notifications to stdout.
type Notifier struct{}

func NewNotifier() *Notifier { return &Notifier{} }

func (n *Notifier) Notify(_ context.Context, notif core.Notification) error {
	log.Printf("[fake/Notifier] to=%s conv=%s spf=%s dkim=%s risk=%d label=%s subject=%q",
		notif.User.Email, notif.ConvID,
		notif.Verdict.SPF, notif.Verdict.DKIM,
		notif.Verdict.Risk, notif.Verdict.Label,
		notif.Email.Subject,
	)
	return nil
}

// MailSender logs outgoing messages to stdout.
type MailSender struct{}

func NewMailSender() *MailSender { return &MailSender{} }

func (s *MailSender) Send(_ context.Context, msg core.OutgoingMessage) error {
	log.Printf("[fake/MailSender] from=%s to=%s subject=%q in-reply-to=%s",
		msg.From, msg.To, msg.Subject, msg.InReplyTo,
	)
	return nil
}

// Signer is a no-op DKIM signer (real signer lands in Etap 2).
type Signer struct{}

func NewSigner() *Signer { return &Signer{} }

func (s *Signer) Sign(_ *core.OutgoingMessage) error {
	log.Println("[fake/Signer] DKIM sign (no-op)")
	return nil
}

// Verdicter always returns a clean verdict (real DNS checks land in Etap 1).
type Verdicter struct{}

func NewVerdicter() *Verdicter { return &Verdicter{} }

func (v *Verdicter) Analyze(_ context.Context, e core.ParsedEmail) core.Verdict {
	log.Printf("[fake/Verdicter] analyzing from=%s subject=%q", e.From, e.Subject)
	return core.Verdict{SPF: "pass", DKIM: "pass", Risk: 1, Label: "clean"}
}
