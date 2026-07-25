package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/jhillyerd/enmime"
	"mailshield/internal/core"
)

type IngestUseCase struct {
	verdicter core.Verdicter
	registry  core.UserRegistry
	store     core.ConversationStore
	notifier  core.Notifier
	aliases   map[string]bool // addresses that expand to all active users (e.g. team@)
}

func NewIngestUseCase(
	v core.Verdicter,
	r core.UserRegistry,
	s core.ConversationStore,
	n core.Notifier,
	aliases ...string,
) *IngestUseCase {
	m := make(map[string]bool, len(aliases))
	for _, a := range aliases {
		m[strings.ToLower(a)] = true
	}
	return &IngestUseCase{verdicter: v, registry: r, store: s, notifier: n, aliases: m}
}

func (uc *IngestUseCase) Ingest(ctx context.Context, raw core.RawEmail) error {
	parsed, err := parseMIME(raw)
	if err != nil {
		return fmt.Errorf("mime parse: %w", err)
	}

	verdict := uc.verdicter.Analyze(ctx, parsed)

	for _, user := range uc.resolveRecipients(raw.To) {
		if err := uc.ingestForUser(ctx, user, parsed, verdict); err != nil {
			return err
		}
	}
	return nil
}

// resolveRecipients expands alias addresses to all active users and deduplicates.
func (uc *IngestUseCase) resolveRecipients(addrs []string) []core.User {
	seen := make(map[core.UserID]bool)
	var result []core.User
	for _, addr := range addrs {
		if uc.aliases[strings.ToLower(addr)] {
			for _, u := range uc.registry.AllActive() {
				if !seen[u.ID] {
					seen[u.ID] = true
					result = append(result, u)
				}
			}
		} else {
			u, ok := uc.registry.ByEmail(addr)
			if ok && !seen[u.ID] {
				seen[u.ID] = true
				result = append(result, u)
			}
		}
	}
	return result
}

func (uc *IngestUseCase) ingestForUser(
	ctx context.Context,
	user core.User,
	parsed core.ParsedEmail,
	verdict core.Verdict,
) error {
	convID := convIDFor(user.ID, parsed.From)

	if _, exists := uc.store.Resolve(convID); !exists {
		thread := core.EmailThread{
			MessageID:  parsed.MessageID,
			References: parsed.References,
			Subject:    parsed.Subject,
			ExtAddr:    parsed.From,
			OwnerID:    user.ID,
		}
		if err := uc.store.Link(convID, thread); err != nil {
			return fmt.Errorf("store link: %w", err)
		}
	}

	return uc.notifier.Notify(ctx, core.Notification{
		User:    user,
		Email:   parsed,
		Verdict: verdict,
		ConvID:  convID,
	})
}

func parseMIME(raw core.RawEmail) (core.ParsedEmail, error) {
	env, err := enmime.ReadEnvelope(strings.NewReader(string(raw.Data)))
	if err != nil {
		return core.ParsedEmail{}, err
	}
	p := core.ParsedEmail{
		SenderIP:  raw.SenderIP,
		From:      raw.From,
		To:        raw.To,
		Subject:   env.GetHeader("Subject"),
		Text:      env.Text,
		HTML:      env.HTML,
		MessageID: env.GetHeader("Message-ID"),
	}
	if refs := env.GetHeader("References"); refs != "" {
		p.References = strings.Fields(refs)
	}
	for _, a := range env.Attachments {
		p.Attachments = append(p.Attachments, core.Attachment{
			Filename:    a.FileName,
			ContentType: a.ContentType,
		})
	}
	return p, nil
}

// convIDFor returns a deterministic ConversationID: one conversation per (owner, external contact).
func convIDFor(owner core.UserID, extAddr string) core.ConversationID {
	return core.ConversationID(fmt.Sprintf("%d:%s", owner, strings.ToLower(strings.TrimSpace(extAddr))))
}
