package session

import (
	"os"
	"sync"
	"time"

	"github.com/abcdlsj/mink/msg"
	"github.com/google/uuid"
)

type Session struct {
	id    string
	store Store
	msgs  []msg.Message
	dirty bool
	mu    sync.Mutex
}

func newSession(id string, store Store) *Session {
	return &Session{id: id, store: store}
}

func (s *Session) ID() string { return s.id }

func (s *Session) Add(m msg.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m.ID == "" {
		m.ID = uuid.New().String()[:8]
	}
	if m.Timestamp.IsZero() {
		m.Timestamp = time.Now()
	}
	s.msgs = append(s.msgs, m)
	s.dirty = true
}

func (s *Session) Messages() []msg.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := make([]msg.Message, len(s.msgs))
	copy(r, s.msgs)
	return r
}

func (s *Session) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dirty {
		return nil
	}
	if err := s.store.Save(s.id, s.msgs); err != nil {
		return err
	}
	s.dirty = false
	return nil
}

func (s *Session) load() error {
	msgs, err := s.store.Load(s.id)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	s.msgs = msgs
	return nil
}
