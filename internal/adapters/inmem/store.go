package inmem

import (
	"sync"

	"mailshield/internal/core"
)

// Store implements core.ConversationStore.
type Store struct {
	mu      sync.RWMutex
	threads map[core.ConversationID]core.EmailThread
}

func NewStore() *Store {
	return &Store{threads: make(map[core.ConversationID]core.EmailThread)}
}

func (s *Store) Link(id core.ConversationID, t core.EmailThread) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.threads[id] = t
	return nil
}

func (s *Store) Resolve(id core.ConversationID) (core.EmailThread, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.threads[id]
	return t, ok
}

// UserRegistry implements core.UserRegistry in memory.
type UserRegistry struct {
	mu      sync.RWMutex
	byEmail map[string]core.User
	byID    map[core.UserID]core.User
}

func NewUserRegistry() *UserRegistry {
	return &UserRegistry{
		byEmail: make(map[string]core.User),
		byID:    make(map[core.UserID]core.User),
	}
}

func (r *UserRegistry) Add(u core.User) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byEmail[u.Email] = u
	r.byID[u.ID] = u
}

func (r *UserRegistry) ByEmail(addr string) (core.User, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	u, ok := r.byEmail[addr]
	return u, ok
}

func (r *UserRegistry) ByID(id core.UserID) (core.User, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	u, ok := r.byID[id]
	return u, ok
}

func (r *UserRegistry) Authorize(actor core.UserID, fromAddr string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	u, ok := r.byID[actor]
	return ok && u.Email == fromAddr
}
