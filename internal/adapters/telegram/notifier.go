package telegram

import (
	"context"
	"fmt"
	"html"
	"log"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"mailshield/internal/core"
)

const maxBodyLen = 2000

// Notifier implements core.Notifier — sends a formatted HTML message and
// records the bot message ID in the shared index for reply detection.
type Notifier struct{ c *Client }

func NewNotifier(c *Client) *Notifier { return &Notifier{c} }

func (n *Notifier) Notify(_ context.Context, notif core.Notification) error {
	if notif.User.TGChatID == 0 {
		log.Printf("[telegram/notifier] skip: TGChatID not set for %s", notif.User.Email)
		return nil
	}

	msg := tgbotapi.NewMessage(notif.User.TGChatID, formatNotification(notif))
	msg.ParseMode = tgbotapi.ModeHTML

	sent, err := n.c.Bot.Send(msg)
	if err != nil {
		return fmt.Errorf("telegram send to %d: %w", notif.User.TGChatID, err)
	}

	// record mapping so Poller can resolve a reply to this message
	if err := n.c.idx.LinkTGMessage(notif.User.TGChatID, sent.MessageID, notif.ConvID); err != nil {
		log.Printf("[telegram/notifier] index link error: %v", err) // non-fatal: message was sent
	}

	log.Printf("[telegram/notifier] sent to %s (chat=%d msg=%d) subject=%q verdict=%s",
		notif.User.Email, notif.User.TGChatID, sent.MessageID,
		notif.Email.Subject, notif.Verdict.Label)
	return nil
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
