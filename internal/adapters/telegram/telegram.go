package telegram

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"mailshield/internal/core"
)

// AdminStore provides admin operations called from the TG admin handler.
type AdminStore interface {
	AllActive() []core.User
	CreateUser(email, displayName string) (core.User, error)
	DeleteUser(email string) error
	SetChatID(email string, chatID int64) error
	CreateBindCode(email string) (string, error)
	ConsumeBindCode(code string) (email string, ok bool)
}

// TopicIndex maps ConversationID ↔ Telegram forum topic ID within a supergroup.
// Implemented by sqlite.Store.
type TopicIndex interface {
	// GetTopicID returns the forum topic ID if one has been created for this conversation.
	GetTopicID(convID core.ConversationID) (topicID int, ok bool)
	// SetTopicID records the forum topic ID after creation (called once per conversation).
	SetTopicID(convID core.ConversationID, topicID int) error
	// ResolveByTopic finds the ConversationID for a given (supergroup chatID, topicID) pair.
	ResolveByTopic(chatID int64, topicID int) (core.ConversationID, bool)
}

// Client is shared between Notifier and Poller.
type Client struct {
	Bot      *tgbotapi.BotAPI
	topicIdx TopicIndex
}

func NewClient(token string, topicIdx TopicIndex) (*Client, error) {
	bot, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, fmt.Errorf("telegram bot init: %w", err)
	}
	slog.Info("telegram authorised", "username", bot.Self.UserName)
	return &Client{Bot: bot, topicIdx: topicIdx}, nil
}

// createTopic creates a forum topic in the given supergroup.
// The bot must be an admin with "Manage Topics" permission.
func (c *Client) createTopic(chatID int64, name string) (int, error) {
	if len(name) > 128 {
		name = name[:128]
	}
	params := tgbotapi.Params{
		"chat_id": strconv.FormatInt(chatID, 10),
		"name":    name,
	}
	resp, err := c.Bot.MakeRequest("createForumTopic", params)
	if err != nil {
		return 0, fmt.Errorf("createForumTopic: %w", err)
	}
	var topic struct {
		MessageThreadID int `json:"message_thread_id"`
	}
	if err := json.Unmarshal(resp.Result, &topic); err != nil {
		return 0, fmt.Errorf("parse createForumTopic response: %w", err)
	}
	return topic.MessageThreadID, nil
}
