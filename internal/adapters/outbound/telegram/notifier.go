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

// Notifier implements core.Notifier by sending a formatted message to a Telegram chat.
// Etap 1: plain sendMessage (no forum topics yet — that's Etap 5).
type Notifier struct {
	bot *tgbotapi.BotAPI
}

func NewNotifier(token string) (*Notifier, error) {
	bot, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, fmt.Errorf("telegram bot init: %w", err)
	}
	log.Printf("[telegram] authorised as @%s", bot.Self.UserName)
	return &Notifier{bot: bot}, nil
}

func (n *Notifier) Notify(_ context.Context, notif core.Notification) error {
	if notif.User.TGChatID == 0 {
		log.Printf("[telegram] skipping notify: TGChatID not set for %s", notif.User.Email)
		return nil
	}

	text := formatNotification(notif)
	msg := tgbotapi.NewMessage(notif.User.TGChatID, text)
	msg.ParseMode = tgbotapi.ModeHTML

	if _, err := n.bot.Send(msg); err != nil {
		return fmt.Errorf("telegram send to %d: %w", notif.User.TGChatID, err)
	}
	log.Printf("[telegram] notified %s (chat=%d) subject=%q verdict=%s",
		notif.User.Email, notif.User.TGChatID, notif.Email.Subject, notif.Verdict.Label)
	return nil
}

func formatNotification(n core.Notification) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("📨 <b>Новое письмо</b>\n\n"))
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

// stripHTML removes HTML tags for plain-text preview.
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
