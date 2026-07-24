package app

import (
	"context"
	"fmt"
	"strings"

	"mailshield/internal/core"
)

type ReplyUseCase struct {
	registry core.UserRegistry
	store    core.ConversationStore
	signer   core.MessageSigner
	sender   core.MailSender
	domain   string
}

func NewReplyUseCase(
	r core.UserRegistry,
	s core.ConversationStore,
	sg core.MessageSigner,
	ml core.MailSender,
	domain string,
) *ReplyUseCase {
	return &ReplyUseCase{registry: r, store: s, signer: sg, sender: ml, domain: domain}
}

func (uc *ReplyUseCase) SubmitReply(ctx context.Context, cmd core.ReplyCommand) error {
	user, ok := uc.registry.ByID(cmd.Actor)
	if !ok {
		return fmt.Errorf("actor %d not found", cmd.Actor)
	}

	thread, ok := uc.store.Resolve(cmd.Conversation)
	if !ok {
		return fmt.Errorf("conversation %q not found", cmd.Conversation)
	}

	if thread.OwnerID != cmd.Actor {
		return fmt.Errorf("actor %d not authorized for conversation %q", cmd.Actor, cmd.Conversation)
	}

	refs := make([]string, len(thread.References))
	copy(refs, thread.References)
	refs = append(refs, thread.MessageID)

	subject := thread.Subject
	if !strings.HasPrefix(subject, "Re:") {
		subject = "Re: " + subject
	}

	msg := core.OutgoingMessage{
		From:       user.Email,
		To:         thread.ExtAddr,
		Subject:    subject,
		Body:       cmd.Body,
		InReplyTo:  thread.MessageID,
		References: refs,
	}

	if err := uc.signer.Sign(&msg); err != nil {
		return fmt.Errorf("dkim sign: %w", err)
	}

	return uc.sender.Send(ctx, msg)
}
