package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
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
	From struct {
		ID int64 `json:"id"`
	} `json:"from"`
}

// Poller is a driving adapter: long-polls Telegram getUpdates and dispatches
// topic messages to ReplyService and direct messages to admin or chat_id helper.
type Poller struct {
	c        *Client
	registry core.UserRegistry
	reply    core.ReplyService
	adminID  int64      // TG user_id of the admin; 0 = admin panel disabled
	admin    AdminStore // nil when adminID == 0
}

func NewPoller(c *Client, registry core.UserRegistry, reply core.ReplyService, adminID int64, admin AdminStore) *Poller {
	return &Poller{c: c, registry: registry, reply: reply, adminID: adminID, admin: admin}
}

func (p *Poller) Run(ctx context.Context) {
	slog.Info("poller started")
	offset := 0

	for {
		select {
		case <-ctx.Done():
			slog.Info("poller stopped")
			return
		default:
		}

		updates, err := p.getUpdates(offset, 30)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				slog.Warn("getUpdates error — retrying", "err", err, "delay", "5s")
				time.Sleep(5 * time.Second)
				continue
			}
		}

		for _, upd := range updates {
			offset = upd.UpdateID + 1
			if upd.Message == nil {
				continue
			}
			p.handleUpdate(ctx, upd.Message)
		}
	}
}

func (p *Poller) handleUpdate(ctx context.Context, msg *tgMessage) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("poller panic recovered", "panic", r, "chat", msg.Chat.ID)
		}
	}()
	switch {
	case strings.HasPrefix(msg.Text, "/bind"):
		// binding runs inside the target supergroup — handle before topic/direct routing
		p.handleBind(msg)
	case msg.MessageThreadID != 0:
		p.handleTopicMessage(ctx, msg)
	default:
		p.handleDirectMessage(msg)
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

// handleTopicMessage routes a forum topic message to the correct conversation.
func (p *Poller) handleTopicMessage(ctx context.Context, msg *tgMessage) {
	chatID := msg.Chat.ID
	threadID := msg.MessageThreadID

	convID, ok := p.c.topicIdx.ResolveByTopic(chatID, threadID)
	if !ok {
		return // message in a topic we don't manage — ignore
	}

	user, ok := p.registry.ByChatID(chatID)
	if !ok {
		slog.Warn("no user for chat_id", "chat", chatID)
		return
	}

	slog.Info("reply", "email", user.Email, "conv", convID)

	if err := p.reply.SubmitReply(ctx, core.ReplyCommand{
		Actor:        user.ID,
		Conversation: convID,
		Body:         msg.Text,
	}); err != nil {
		slog.Error("submit reply failed", "err", err)
	}
}

// handleBind consumes a bind code sent inside the target supergroup and links
// that mailbox to this chat. The code itself is the authorization — anyone who
// received it from the admin can complete the bind for their own group.
func (p *Poller) handleBind(msg *tgMessage) {
	parts := strings.Fields(msg.Text)
	if len(parts) != 2 {
		p.sendMsg(msg.Chat.ID, "Использование: <code>/bind КОД</code>")
		return
	}
	if msg.Chat.ID > 0 {
		p.sendMsg(msg.Chat.ID, "❌ Отправьте <code>/bind</code> в супергруппе (с топиками), а не в личке — топики живут только в группе.")
		return
	}
	email, ok := p.admin.ConsumeBindCode(parts[1])
	if !ok {
		p.sendMsg(msg.Chat.ID, "❌ Неверный или уже использованный код.")
		return
	}
	if err := p.admin.SetChatID(email, msg.Chat.ID); err != nil {
		p.sendMsg(msg.Chat.ID, "❌ Ошибка: "+err.Error())
		return
	}
	p.sendMsg(msg.Chat.ID, fmt.Sprintf(
		"✅ <code>%s</code> привязан к этой группе (chat_id <code>%d</code>).\nПисьма для этого адреса теперь приходят сюда отдельными топиками.",
		email, msg.Chat.ID,
	))
	slog.Info("bind code consumed", "email", email, "chat", msg.Chat.ID)
}

// handleDirectMessage handles non-topic DMs. Admin commands go to the admin
// handler; everyone else gets their chat_id back (useful for onboarding).
func (p *Poller) handleDirectMessage(msg *tgMessage) {
	isAdmin := p.adminID != 0 && msg.From.ID == p.adminID && msg.Chat.ID > 0
	if isAdmin && strings.HasPrefix(msg.Text, "/") {
		p.handleAdminCommand(msg)
		return
	}
	p.sendMsg(msg.Chat.ID, fmt.Sprintf(
		"Your chat_id: <code>%d</code>\n\nForward this to the admin to activate your account.",
		msg.Chat.ID,
	))
}

const adminHelp = `<b>Admin commands</b>

/adduser email [name] — создать ящик и выдать код привязки
/bind КОД — (в супергруппе) привязать ящик к этой группе
/users — список ящиков и их статус
/setchat email chat_id — привязать вручную
/deluser email — удалить ящик
/help — эта справка`

// handleAdminCommand executes admin commands sent in a DM from the configured admin.
func (p *Poller) handleAdminCommand(msg *tgMessage) {
	parts := strings.Fields(msg.Text)
	if len(parts) == 0 {
		return
	}
	chatID := msg.Chat.ID

	switch parts[0] {
	case "/adduser":
		if len(parts) < 2 {
			p.sendMsg(chatID, "Использование: <code>/adduser email [name]</code>")
			return
		}
		email := parts[1]
		name := ""
		if len(parts) >= 3 {
			name = strings.Join(parts[2:], " ")
		}
		user, err := p.admin.CreateUser(email, name)
		if err != nil {
			p.sendMsg(chatID, "❌ "+err.Error())
			return
		}
		code, err := p.admin.CreateBindCode(user.Email)
		if err != nil {
			p.sendMsg(chatID, fmt.Sprintf("✅ Ящик <code>%s</code> создан (id %d), но код не выдан: %v", user.Email, user.ID, err))
			return
		}
		p.sendMsg(chatID, fmt.Sprintf(
			"✅ Ящик <code>%s</code> создан.\n\nКод привязки: <code>%s</code>\nОтправьте <code>/bind %s</code> в супергруппе этого пользователя, чтобы направлять туда его почту.",
			user.Email, code, code,
		))
		slog.Info("mailbox created", "email", user.Email, "user_id", user.ID)

	case "/deluser":
		if len(parts) != 2 {
			p.sendMsg(chatID, "Использование: <code>/deluser email</code>")
			return
		}
		if err := p.admin.DeleteUser(parts[1]); err != nil {
			p.sendMsg(chatID, "❌ "+err.Error())
			return
		}
		p.sendMsg(chatID, "✅ Ящик <code>"+parts[1]+"</code> удалён.")
		slog.Info("mailbox deleted", "email", parts[1])

	case "/users":
		users := p.admin.AllActive()
		if len(users) == 0 {
			p.sendMsg(chatID, "Ящиков пока нет. Создайте: <code>/adduser email [name]</code>")
			return
		}
		var sb strings.Builder
		sb.WriteString("<b>Ящики:</b>\n\n")
		for _, u := range users {
			if u.TGChatID != 0 {
				sb.WriteString(fmt.Sprintf("✅ <code>%s</code>\n   chat_id: <code>%d</code>\n\n", u.Email, u.TGChatID))
			} else {
				sb.WriteString(fmt.Sprintf("⚠️ <code>%s</code>\n   не привязан — /adduser выдаст код\n\n", u.Email))
			}
		}
		p.sendMsg(chatID, sb.String())

	case "/setchat":
		if len(parts) != 3 {
			p.sendMsg(chatID, "Использование: <code>/setchat email chat_id</code>")
			return
		}
		cid, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil {
			p.sendMsg(chatID, "❌ Некорректный chat_id: <code>"+parts[2]+"</code>")
			return
		}
		if err := p.admin.SetChatID(parts[1], cid); err != nil {
			p.sendMsg(chatID, "❌ "+err.Error())
			return
		}
		p.sendMsg(chatID, fmt.Sprintf("✅ <code>%s</code> → <code>%d</code>", parts[1], cid))
		slog.Info("chat_id set", "email", parts[1], "chat", cid)

	default:
		p.sendMsg(chatID, adminHelp)
	}
}

func (p *Poller) sendMsg(chatID int64, text string) {
	params := tgbotapi.Params{
		"chat_id":    strconv.FormatInt(chatID, 10),
		"text":       text,
		"parse_mode": "HTML",
	}
	if _, err := p.c.Bot.MakeRequest("sendMessage", params); err != nil {
		slog.Warn("sendMsg error", "chat", chatID, "err", err)
	}
}
