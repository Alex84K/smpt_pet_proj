package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"mailshield/internal/core"
)

// tgUpdate is a minimal raw Telegram update struct.
// We parse it manually because tgbotapi v5.5.1 predates forum topics
// and its Message struct lacks MessageThreadID.
type tgUpdate struct {
	UpdateID int        `json:"update_id"`
	Message  *tgMessage `json:"message"`
}

type tgMessage struct {
	MessageID       int    `json:"message_id"`
	MessageThreadID int    `json:"message_thread_id"`
	Text            string `json:"text"`
	Chat            struct {
		ID int64 `json:"id"`
	} `json:"chat"`
}

// Poller is a driving adapter: long-polls Telegram getUpdates and dispatches
// topic messages to ReplyService, and non-topic messages to a chat_id helper.
type Poller struct {
	c        *Client
	registry core.UserRegistry
	reply    core.ReplyService
}

func NewPoller(c *Client, registry core.UserRegistry, reply core.ReplyService) *Poller {
	return &Poller{c: c, registry: registry, reply: reply}
}

func (p *Poller) Run(ctx context.Context) {
	log.Println("[telegram/poller] started (forum topics mode)")
	offset := 0

	for {
		select {
		case <-ctx.Done():
			log.Println("[telegram/poller] stopped")
			return
		default:
		}

		updates, err := p.getUpdates(offset, 30)
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
			if upd.Message == nil {
				continue
			}
			if upd.Message.MessageThreadID != 0 {
				p.handleTopicMessage(ctx, upd.Message)
			} else {
				p.handleChatIDQuery(upd.Message)
			}
		}
	}
}

func (p *Poller) getUpdates(offset, timeout int) ([]tgUpdate, error) {
	params := tgbotapi.Params{"timeout": strconv.Itoa(timeout)}
	if offset > 0 {
		params["offset"] = strconv.Itoa(offset)
	}
	resp, err := p.c.Bot.MakeRequest("getUpdates", params)
	if err != nil {
		return nil, err
	}
	var updates []tgUpdate
	if err := json.Unmarshal(resp.Result, &updates); err != nil {
		return nil, fmt.Errorf("parse updates: %w", err)
	}
	return updates, nil
}

// handleTopicMessage routes a message from a forum topic to the correct conversation.
func (p *Poller) handleTopicMessage(ctx context.Context, msg *tgMessage) {
	chatID := msg.Chat.ID
	threadID := msg.MessageThreadID

	convID, ok := p.c.topicIdx.ResolveByTopic(chatID, threadID)
	if !ok {
		return // message in a topic we don't manage — ignore
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

// handleChatIDQuery responds to any non-topic message with the sender's chat_id.
// Useful for new users discovering their chat_id to share with the admin.
func (p *Poller) handleChatIDQuery(msg *tgMessage) {
	text := fmt.Sprintf(
		"Your chat_id: <code>%d</code>\n\nForward this to the admin to activate your account.",
		msg.Chat.ID,
	)
	params := tgbotapi.Params{
		"chat_id":    strconv.FormatInt(msg.Chat.ID, 10),
		"text":       text,
		"parse_mode": "HTML",
	}
	if _, err := p.c.Bot.MakeRequest("sendMessage", params); err != nil {
		log.Printf("[telegram/poller] chat_id reply error: %v", err)
	}
}
