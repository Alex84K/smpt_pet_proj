package telegram

import (
	"context"
	"log"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"mailshield/internal/core"
)

// Poller is a driving adapter: long-polls Telegram getUpdates and calls
// ReplyService when a user replies to a bot notification message.
type Poller struct {
	c        *Client
	registry core.UserRegistry
	reply    core.ReplyService
}

func NewPoller(c *Client, registry core.UserRegistry, reply core.ReplyService) *Poller {
	return &Poller{c: c, registry: registry, reply: reply}
}

func (p *Poller) Run(ctx context.Context) {
	log.Println("[telegram/poller] started")
	offset := 0

	for {
		select {
		case <-ctx.Done():
			log.Println("[telegram/poller] stopped")
			return
		default:
		}

		updates, err := p.c.Bot.GetUpdates(tgbotapi.UpdateConfig{
			Offset:  offset,
			Timeout: 30,
		})
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				log.Printf("[telegram/poller] getUpdates error: %v — retrying in 5s", err)
				time.Sleep(5 * time.Second)
				continue
			}
		}

		for _, upd := range updates {
			offset = upd.UpdateID + 1
			if upd.Message != nil && upd.Message.ReplyToMessage != nil {
				p.handleReply(ctx, upd.Message)
			}
		}
	}
}

func (p *Poller) handleReply(ctx context.Context, msg *tgbotapi.Message) {
	chatID := msg.Chat.ID
	repliedToID := msg.ReplyToMessage.MessageID

	convID, ok := p.c.idx.ResolveTGMessage(chatID, repliedToID)
	if !ok {
		// reply to something other than a bot notification — ignore
		return
	}

	user, ok := p.registry.ByChatID(chatID)
	if !ok {
		log.Printf("[telegram/poller] no user for chat_id=%d", chatID)
		return
	}

	log.Printf("[telegram/poller] reply from %s conv=%s body=%q", user.Email, convID, msg.Text)

	if err := p.reply.SubmitReply(ctx, core.ReplyCommand{
		Actor:        user.ID,
		Conversation: convID,
		Body:         msg.Text,
	}); err != nil {
		log.Printf("[telegram/poller] submit reply error: %v", err)
	}
}
