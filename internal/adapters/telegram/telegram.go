package telegram

import (
	"fmt"
	"log"
	"sync"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"mailshield/internal/core"
)

// MessageIndex maps (chatID, botMsgID) → ConversationID for reply detection.
// Implemented by sqlite.Store (persistent) or inmemIndex (dev/test).
type MessageIndex interface {
	LinkTGMessage(chatID int64, msgID int, convID core.ConversationID) error
	ResolveTGMessage(chatID int64, msgID int) (core.ConversationID, bool)
}

// Client is shared between Notifier and Poller.
type Client struct {
	Bot *tgbotapi.BotAPI
	idx MessageIndex
}

func NewClient(token string, idx MessageIndex) (*Client, error) {
	bot, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, fmt.Errorf("telegram bot init: %w", err)
	}
	log.Printf("[telegram] authorised as @%s", bot.Self.UserName)
	return &Client{Bot: bot, idx: idx}, nil
}

// NewInMemIndex returns a non-persistent in-memory MessageIndex (useful in tests).
func NewInMemIndex() MessageIndex { return newInmemIndex() }

// inmemIndex is the in-memory fallback implementation.
type inmemIndex struct {
	mu   sync.RWMutex
	data map[msgKey]core.ConversationID
}

type msgKey struct {
	chatID int64
	msgID  int
}

func newInmemIndex() *inmemIndex {
	return &inmemIndex{data: make(map[msgKey]core.ConversationID)}
}

func (i *inmemIndex) LinkTGMessage(chatID int64, msgID int, convID core.ConversationID) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.data[msgKey{chatID, msgID}] = convID
	return nil
}

func (i *inmemIndex) ResolveTGMessage(chatID int64, msgID int) (core.ConversationID, bool) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	c, ok := i.data[msgKey{chatID, msgID}]
	return c, ok
}
