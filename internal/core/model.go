package core

// UserID is the stable identifier for a mailbox owner.
type UserID int64

// ConversationID is the domain identifier for an email thread per owner.
type ConversationID string

type RawEmail struct {
	SenderIP string
	From     string
	To       []string
	Data     []byte
}

type ParsedEmail struct {
	SenderIP    string // from SMTP envelope (for SPF check)
	From        string
	To          []string
	Subject     string
	Text        string
	HTML        string
	MessageID   string
	References  []string
	Attachments []Attachment
}

type Attachment struct {
	Filename    string
	ContentType string
}

type Verdict struct {
	SPF   string // pass / fail / softfail / none
	DKIM  string // pass / fail / none
	Risk  int    // 1..10
	Label string // clean / suspicious / malicious
}

type User struct {
	ID          UserID
	Email       string
	DisplayName string
	TGChatID    int64
}

// EmailThread holds the email headers needed to continue a thread.
type EmailThread struct {
	MessageID  string
	References []string
	Subject    string
	ExtAddr    string // external participant's address
	OwnerID    UserID
}

// Notification is what the Notifier receives after analysis.
type Notification struct {
	User    User
	Email   ParsedEmail
	Verdict Verdict
	ConvID  ConversationID
}

// ReplyCommand is the transport-neutral input for ReplyService.
// The driving adapter translates its own identifiers into UserID + ConversationID.
type ReplyCommand struct {
	Actor        UserID
	Conversation ConversationID
	Body         string
	Attachments  []Attachment
}

// OutgoingMessage is a fully-addressed, ready-to-sign email.
type OutgoingMessage struct {
	From       string
	To         string
	Subject    string
	Body       string
	InReplyTo  string
	References []string
}
