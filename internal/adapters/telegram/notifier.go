package telegram

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"mailshield/internal/core"
)

const maxBodyLen = 2000

// Notifier implements core.Notifier — sends formatted HTML to a forum topic,
// creating the topic on the first message from a new contact.
type Notifier struct{ c *Client }

func NewNotifier(c *Client) *Notifier { return &Notifier{c} }

func (n *Notifier) Notify(_ context.Context, notif core.Notification) error {
	if notif.User.TGChatID == 0 {
		slog.Warn("notify skip: TGChatID not set", "email", notif.User.Email)
		return nil
	}

	chatID := notif.User.TGChatID

	topicID, ok := n.c.topicIdx.GetTopicID(notif.ConvID)
	if !ok {
		// First email from this contact — create a dedicated forum topic.
		id, err := n.c.createTopic(chatID, topicName(notif))
		if err != nil {
			slog.Warn("create topic failed — sending to general chat", "conv", notif.ConvID, "err", err)
		} else {
			_ = n.c.topicIdx.SetTopicID(notif.ConvID, id)
			topicID = id
		}
	}

	params := tgbotapi.Params{
		"chat_id":    strconv.FormatInt(chatID, 10),
		"text":       formatNotification(notif),
		"parse_mode": "HTML",
	}
	if topicID > 0 {
		params["message_thread_id"] = strconv.Itoa(topicID)
	}

	if _, err := n.c.Bot.MakeRequest("sendMessage", params); err != nil {
		return fmt.Errorf("telegram sendMessage to %d topic=%d: %w", chatID, topicID, err)
	}

	slog.Info("notify sent", "email", notif.User.Email, "chat", chatID, "topic", topicID,
		"subject", notif.Email.Subject, "verdict", notif.Verdict.Label)
	return nil
}

func topicName(notif core.Notification) string {
	if notif.Email.From != "" {
		return notif.Email.From
	}
	return string(notif.ConvID)
}

func formatNotification(n core.Notification) string {
	var sb strings.Builder

	sb.WriteString("📨 <b>Новое письмо</b>\n\n")
	sb.WriteString(fmt.Sprintf("<b>От:</b> <code>%s</code>\n", html.EscapeString(n.Email.From)))
	sb.WriteString(fmt.Sprintf("<b>Кому:</b> <code>%s</code>\n", html.EscapeString(n.User.Email)))
	sb.WriteString(fmt.Sprintf("<b>Тема:</b> %s\n\n", html.EscapeString(n.Email.Subject)))
	sb.WriteString(fmt.Sprintf("🛡 SPF: <code>%s</code> | DKIM: <code>%s</code> | Риск: <code>%d/10</code> %s\n",
		n.Verdict.SPF, n.Verdict.DKIM, n.Verdict.Risk, verdictBadge(n.Verdict.Label),
	))

	body := n.Email.Text
	if body == "" {
		body = stripHTML(n.Email.HTML)
	}
	if body != "" {
		sb.WriteString("\n─────────────\n")
		if len(body) > maxBodyLen {
			body = body[:maxBodyLen] + "…"
		}
		sb.WriteString(html.EscapeString(strings.TrimSpace(body)))
	}

	return sb.String()
}

func verdictBadge(label string) string {
	switch label {
	case "suspicious":
		return "⚠️"
	case "malicious":
		return "🚫"
	default:
		return "✅"
	}
}

func stripHTML(s string) string {
	var out strings.Builder
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		case !inTag:
			out.WriteRune(r)
		}
	}
	return out.String()
}
