package sqlite

import (
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"

	_ "modernc.org/sqlite"
	"mailshield/internal/core"
)

// Store implements core.ConversationStore, core.UserRegistry,
// and telegram.MessageIndex in a single SQLite database.
type Store struct{ db *sql.DB }

func New(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", path, err)
	}
	db.SetMaxOpenConns(1) // WAL: one writer is enough for MVP
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	log.Printf("[sqlite] ready at %s", path)
	return s, nil
}

func (s *Store) migrate() error {
	stmts := []string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA busy_timeout=5000`,
		`PRAGMA foreign_keys=ON`,
		`CREATE TABLE IF NOT EXISTS users (
			id           INTEGER PRIMARY KEY,
			email        TEXT    NOT NULL UNIQUE,
			display_name TEXT    NOT NULL DEFAULT '',
			tg_chat_id   INTEGER NOT NULL DEFAULT 0,
			active       INTEGER NOT NULL DEFAULT 1
		)`,
		`CREATE TABLE IF NOT EXISTS conversations (
			id              TEXT    PRIMARY KEY,
			owner_user_id   INTEGER NOT NULL REFERENCES users(id),
			external_addr   TEXT    NOT NULL,
			subject         TEXT    NOT NULL DEFAULT '',
			root_message_id TEXT    NOT NULL DEFAULT '',
			refs            TEXT    NOT NULL DEFAULT '',
			tg_topic_id     INTEGER NOT NULL DEFAULT 0,
			created_at      TEXT    NOT NULL,
			UNIQUE(owner_user_id, external_addr)
		)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("exec %q: %w", stmt[:min(40, len(stmt))], err)
		}
	}
	// idempotent column additions for existing DBs
	s.db.Exec(`ALTER TABLE conversations ADD COLUMN tg_topic_id INTEGER NOT NULL DEFAULT 0`)
	return nil
}

// ---- UserRegistry ----

// AddUser inserts a user only if they don't exist yet (idempotent seed).
// To change a user's chat_id after first run, update the DB directly.
func (s *Store) AddUser(u core.User) error {
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO users (id, email, display_name, tg_chat_id, active)
		 VALUES (?, ?, ?, ?, 1)`,
		int64(u.ID), u.Email, u.DisplayName, u.TGChatID,
	)
	return err
}

func (s *Store) ByEmail(addr string) (core.User, bool) {
	return s.queryUser(
		`SELECT id, email, display_name, tg_chat_id FROM users WHERE email=? AND active=1`,
		addr,
	)
}

func (s *Store) ByID(id core.UserID) (core.User, bool) {
	return s.queryUser(
		`SELECT id, email, display_name, tg_chat_id FROM users WHERE id=? AND active=1`,
		int64(id),
	)
}

func (s *Store) ByChatID(chatID int64) (core.User, bool) {
	return s.queryUser(
		`SELECT id, email, display_name, tg_chat_id FROM users WHERE tg_chat_id=? AND active=1`,
		chatID,
	)
}

func (s *Store) AllActive() []core.User {
	rows, err := s.db.Query(`SELECT id, email, display_name, tg_chat_id FROM users WHERE active=1`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var users []core.User
	for rows.Next() {
		var u core.User
		var id int64
		if err := rows.Scan(&id, &u.Email, &u.DisplayName, &u.TGChatID); err != nil {
			continue
		}
		u.ID = core.UserID(id)
		users = append(users, u)
	}
	return users
}

func (s *Store) Authorize(actor core.UserID, fromAddr string) bool {
	u, ok := s.ByID(actor)
	return ok && u.Email == fromAddr
}

func (s *Store) queryUser(query string, arg any) (core.User, bool) {
	var u core.User
	var id int64
	err := s.db.QueryRow(query, arg).Scan(&id, &u.Email, &u.DisplayName, &u.TGChatID)
	if err != nil {
		return core.User{}, false
	}
	u.ID = core.UserID(id)
	return u, true
}

// ---- ConversationStore ----

func (s *Store) Link(id core.ConversationID, t core.EmailThread) error {
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO conversations
			(id, owner_user_id, external_addr, subject, root_message_id, refs, tg_topic_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, 0, ?)`,
		string(id), int64(t.OwnerID), t.ExtAddr, t.Subject,
		t.MessageID, strings.Join(t.References, " "),
		time.Now().UTC().Format(time.RFC3339),
	)
	return err
}

func (s *Store) Resolve(id core.ConversationID) (core.EmailThread, bool) {
	var t core.EmailThread
	var ownerID int64
	var refs string
	err := s.db.QueryRow(
		`SELECT owner_user_id, external_addr, subject, root_message_id, refs
		 FROM conversations WHERE id=?`,
		string(id),
	).Scan(&ownerID, &t.ExtAddr, &t.Subject, &t.MessageID, &refs)
	if err != nil {
		return core.EmailThread{}, false
	}
	t.OwnerID = core.UserID(ownerID)
	if refs != "" {
		t.References = strings.Fields(refs)
	}
	return t, true
}

// ---- telegram.TopicIndex ----

func (s *Store) GetTopicID(convID core.ConversationID) (int, bool) {
	var id int
	err := s.db.QueryRow(
		`SELECT tg_topic_id FROM conversations WHERE id=?`, string(convID),
	).Scan(&id)
	if err != nil || id == 0 {
		return 0, false
	}
	return id, true
}

func (s *Store) SetTopicID(convID core.ConversationID, topicID int) error {
	_, err := s.db.Exec(
		`UPDATE conversations SET tg_topic_id=? WHERE id=?`, topicID, string(convID),
	)
	return err
}

func (s *Store) ResolveByTopic(chatID int64, topicID int) (core.ConversationID, bool) {
	var convID string
	err := s.db.QueryRow(`
		SELECT c.id FROM conversations c
		JOIN users u ON c.owner_user_id = u.id
		WHERE c.tg_topic_id = ? AND u.tg_chat_id = ?`,
		topicID, chatID,
	).Scan(&convID)
	if err != nil {
		return "", false
	}
	return core.ConversationID(convID), true
}
