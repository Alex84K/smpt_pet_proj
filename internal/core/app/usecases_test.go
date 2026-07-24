package app_test

import (
	"context"
	"testing"

	"mailshield/internal/core"
	"mailshield/internal/core/app"
)

// ---- shared test fakes ----

type fakeVerdicter struct{}

func (fakeVerdicter) Analyze(_ context.Context, _ core.ParsedEmail) core.Verdict {
	return core.Verdict{SPF: "pass", DKIM: "pass", Risk: 1, Label: "clean"}
}

type fakeRegistry struct{ users map[string]core.User }

func newFakeRegistry(users ...core.User) *fakeRegistry {
	m := make(map[string]core.User, len(users))
	for _, u := range users {
		m[u.Email] = u
	}
	return &fakeRegistry{users: m}
}

func (r *fakeRegistry) ByEmail(addr string) (core.User, bool) {
	u, ok := r.users[addr]
	return u, ok
}

func (r *fakeRegistry) ByID(id core.UserID) (core.User, bool) {
	for _, u := range r.users {
		if u.ID == id {
			return u, true
		}
	}
	return core.User{}, false
}

func (r *fakeRegistry) ByChatID(chatID int64) (core.User, bool) {
	for _, u := range r.users {
		if u.TGChatID == chatID {
			return u, true
		}
	}
	return core.User{}, false
}

func (r *fakeRegistry) Authorize(actor core.UserID, fromAddr string) bool {
	u, ok := r.ByID(actor)
	return ok && u.Email == fromAddr
}

type fakeStore struct{ threads map[core.ConversationID]core.EmailThread }

func newFakeStore() *fakeStore {
	return &fakeStore{threads: make(map[core.ConversationID]core.EmailThread)}
}

func (s *fakeStore) Link(id core.ConversationID, t core.EmailThread) error {
	s.threads[id] = t
	return nil
}

func (s *fakeStore) Resolve(id core.ConversationID) (core.EmailThread, bool) {
	t, ok := s.threads[id]
	return t, ok
}

type notifySpy struct {
	count int
	last  core.Notification
}

func (s *notifySpy) Notify(_ context.Context, n core.Notification) error {
	s.count++
	s.last = n
	return nil
}

type fakeSigner struct{}

func (fakeSigner) Sign(_ *core.OutgoingMessage) error { return nil }

type sendSpy struct {
	count int
	last  core.OutgoingMessage
}

func (s *sendSpy) Send(_ context.Context, msg core.OutgoingMessage) error {
	s.count++
	s.last = msg
	return nil
}

// ---- shared fixtures ----

var boris = core.User{ID: 1, Email: "boris@shk.solutions", TGChatID: -100111}
var fima = core.User{ID: 2, Email: "fima@shk.solutions", TGChatID: -100222}

const rawEmailFixture = "From: client@acme.com\r\nTo: boris@shk.solutions\r\n" +
	"Subject: Test\r\nMessage-ID: <abc@acme.com>\r\n\r\nHello Boris!"

// ---- IngestUseCase tests ----

func TestIngest_KnownRecipient_Notified(t *testing.T) {
	spy := &notifySpy{}
	uc := app.NewIngestUseCase(fakeVerdicter{}, newFakeRegistry(boris), newFakeStore(), spy)

	err := uc.Ingest(context.Background(), core.RawEmail{
		SenderIP: "1.2.3.4",
		From:     "client@acme.com",
		To:       []string{"boris@shk.solutions"},
		Data:     []byte(rawEmailFixture),
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spy.count != 1 {
		t.Fatalf("want 1 notification, got %d", spy.count)
	}
	if spy.last.User.ID != boris.ID {
		t.Errorf("want notification for user %d, got %d", boris.ID, spy.last.User.ID)
	}
	if spy.last.Verdict.SPF != "pass" {
		t.Errorf("want SPF=pass, got %q", spy.last.Verdict.SPF)
	}
}

func TestIngest_UnknownRecipient_Skipped(t *testing.T) {
	spy := &notifySpy{}
	uc := app.NewIngestUseCase(fakeVerdicter{}, newFakeRegistry(), newFakeStore(), spy)

	err := uc.Ingest(context.Background(), core.RawEmail{
		SenderIP: "1.2.3.4",
		From:     "client@acme.com",
		To:       []string{"unknown@shk.solutions"},
		Data:     []byte(rawEmailFixture),
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spy.count != 0 {
		t.Errorf("want 0 notifications for unknown recipient, got %d", spy.count)
	}
}

func TestIngest_SameContact_ConversationLinkedOnce(t *testing.T) {
	spy := &notifySpy{}
	store := newFakeStore()
	uc := app.NewIngestUseCase(fakeVerdicter{}, newFakeRegistry(boris), store, spy)

	raw := core.RawEmail{
		SenderIP: "1.2.3.4",
		From:     "client@acme.com",
		To:       []string{"boris@shk.solutions"},
		Data:     []byte(rawEmailFixture),
	}

	_ = uc.Ingest(context.Background(), raw)
	_ = uc.Ingest(context.Background(), raw)

	if len(store.threads) != 1 {
		t.Errorf("want 1 conversation in store, got %d", len(store.threads))
	}
	if spy.count != 2 {
		t.Errorf("want 2 notifications (one per email), got %d", spy.count)
	}
}

func TestIngest_FanOut_TwoRecipients_BothNotified(t *testing.T) {
	spy := &notifySpy{}
	uc := app.NewIngestUseCase(fakeVerdicter{}, newFakeRegistry(boris, fima), newFakeStore(), spy)

	raw := core.RawEmail{
		SenderIP: "1.2.3.4",
		From:     "client@acme.com",
		To:       []string{"boris@shk.solutions", "fima@shk.solutions"},
		Data:     []byte("From: client@acme.com\r\nTo: boris@shk.solutions, fima@shk.solutions\r\nSubject: Hello\r\nMessage-ID: <xyz@acme.com>\r\n\r\nTeam mail!"),
	}

	if err := uc.Ingest(context.Background(), raw); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spy.count != 2 {
		t.Errorf("want 2 notifications (fan-out), got %d", spy.count)
	}
}

// ---- ReplyUseCase tests ----

func TestReply_SendsFromOwnerAddress(t *testing.T) {
	store := newFakeStore()
	_ = store.Link("conv:1", core.EmailThread{
		MessageID: "<abc@acme.com>",
		Subject:   "Test",
		ExtAddr:   "client@acme.com",
		OwnerID:   boris.ID,
	})

	spy := &sendSpy{}
	uc := app.NewReplyUseCase(newFakeRegistry(boris), store, fakeSigner{}, spy, "shk.solutions")

	err := uc.SubmitReply(context.Background(), core.ReplyCommand{
		Actor:        boris.ID,
		Conversation: "conv:1",
		Body:         "Hello back!",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spy.last.From != boris.Email {
		t.Errorf("want From=%q, got %q", boris.Email, spy.last.From)
	}
	if spy.last.To != "client@acme.com" {
		t.Errorf("want To=client@acme.com, got %q", spy.last.To)
	}
	if spy.last.InReplyTo != "<abc@acme.com>" {
		t.Errorf("want InReplyTo=<abc@acme.com>, got %q", spy.last.InReplyTo)
	}
}

func TestReply_SubjectPrefixed(t *testing.T) {
	store := newFakeStore()
	_ = store.Link("conv:1", core.EmailThread{
		MessageID: "<abc@acme.com>",
		Subject:   "Test",
		ExtAddr:   "client@acme.com",
		OwnerID:   boris.ID,
	})

	spy := &sendSpy{}
	uc := app.NewReplyUseCase(newFakeRegistry(boris), store, fakeSigner{}, spy, "shk.solutions")
	_ = uc.SubmitReply(context.Background(), core.ReplyCommand{Actor: boris.ID, Conversation: "conv:1", Body: "ok"})

	if spy.last.Subject != "Re: Test" {
		t.Errorf("want subject %q, got %q", "Re: Test", spy.last.Subject)
	}
}

func TestReply_WrongOwner_Rejected(t *testing.T) {
	store := newFakeStore()
	_ = store.Link("conv:1", core.EmailThread{
		MessageID: "<abc@acme.com>",
		ExtAddr:   "client@acme.com",
		OwnerID:   boris.ID, // Boris owns this conversation
	})

	uc := app.NewReplyUseCase(newFakeRegistry(boris, fima), store, fakeSigner{}, &sendSpy{}, "shk.solutions")

	err := uc.SubmitReply(context.Background(), core.ReplyCommand{
		Actor:        fima.ID, // Fima tries to reply to Boris's conversation
		Conversation: "conv:1",
		Body:         "I should not send this",
	})

	if err == nil {
		t.Fatal("expected authorization error, got nil")
	}
}

func TestReply_UnknownConversation_Rejected(t *testing.T) {
	uc := app.NewReplyUseCase(newFakeRegistry(boris), newFakeStore(), fakeSigner{}, &sendSpy{}, "shk.solutions")

	err := uc.SubmitReply(context.Background(), core.ReplyCommand{
		Actor:        boris.ID,
		Conversation: "conv:nonexistent",
		Body:         "hello",
	})

	if err == nil {
		t.Fatal("expected error for unknown conversation, got nil")
	}
}
