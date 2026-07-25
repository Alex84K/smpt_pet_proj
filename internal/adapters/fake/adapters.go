package fake

import (
	"context"
	"log/slog"

	"mailshield/internal/core"
)

// Notifier logs notifications to stdout.
type Notifier struct{}

func NewNotifier() *Notifier { return &Notifier{} }

func (n *Notifier) Notify(_ context.Context, notif core.Notification) error {
	slog.Info("fake notify", "to", notif.User.Email, "conv", notif.ConvID,
		"spf", notif.Verdict.SPF, "risk", notif.Verdict.Risk, "subject", notif.Email.Subject)
	return nil
}

// MailSender logs outgoing messages to stdout.
type MailSender struct{}

func NewMailSender() *MailSender { return &MailSender{} }

func (s *MailSender) Send(_ context.Context, msg core.OutgoingMessage) error {
	slog.Info("fake send", "from", msg.From, "to", msg.To, "subject", msg.Subject)
	return nil
}

// Signer is a no-op DKIM signer (real signer lands in Etap 2).
type Signer struct{}

func NewSigner() *Signer { return &Signer{} }

func (s *Signer) Sign(_ *core.OutgoingMessage) error {
	return nil
}

// Verdicter always returns a clean verdict (real DNS checks land in Etap 1).
type Verdicter struct{}

func NewVerdicter() *Verdicter { return &Verdicter{} }

func (v *Verdicter) Analyze(_ context.Context, e core.ParsedEmail) core.Verdict {
	slog.Info("fake analyze", "from", e.From, "subject", e.Subject)
	return core.Verdict{SPF: "pass", DKIM: "pass", Risk: 1, Label: "clean"}
}
