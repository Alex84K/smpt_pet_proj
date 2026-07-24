package core

import "context"

// ---- DRIVING (primary) ports ----

type MailIngestor interface {
	Ingest(ctx context.Context, raw RawEmail) error
}

type ReplyService interface {
	SubmitReply(ctx context.Context, cmd ReplyCommand) error
}

// ---- DRIVEN (secondary) ports ----

type Notifier interface {
	Notify(ctx context.Context, n Notification) error
}

type MailSender interface {
	Send(ctx context.Context, msg OutgoingMessage) error
}

// MessageSigner signs an OutgoingMessage in place (DKIM).
type MessageSigner interface {
	Sign(msg *OutgoingMessage) error
}

type ConversationStore interface {
	Link(id ConversationID, thread EmailThread) error
	Resolve(id ConversationID) (EmailThread, bool)
}

type UserRegistry interface {
	ByEmail(addr string) (User, bool)
	ByID(id UserID) (User, bool)
	// Authorize returns true when actor is allowed to send from fromAddr.
	Authorize(actor UserID, fromAddr string) bool
}

type Verdicter interface {
	Analyze(ctx context.Context, e ParsedEmail) Verdict
}
