package session

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

type Store interface {
	SaveSession(*Session) error
	LoadSession(string) (*Session, error)
	ListSessions() ([]*Session, error)
	CurrentSessionID(string) (string, error)
	SetCurrentSession(string, string) error
}

type Manager struct {
	store    Store
	sessions map[string]*Session
	currents map[string]string
	mu       sync.RWMutex
}

func NewManager(store Store) *Manager {
	return &Manager{
		store:    store,
		sessions: map[string]*Session{},
		currents: map[string]string{},
	}
}

func (m *Manager) Current(source string) (*Session, error) {
	source = normalizeSource(source)
	m.mu.RLock()
	if id := m.currents[source]; id != "" {
		if s := m.sessions[id]; s != nil {
			m.mu.RUnlock()
			return s, nil
		}
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	if id := m.currents[source]; id != "" {
		if s := m.sessions[id]; s != nil {
			return s, nil
		}
	}
	if id, err := m.store.CurrentSessionID(source); err == nil && id != "" {
		s, err := m.store.LoadSession(id)
		if err != nil {
			return nil, err
		}
		m.sessions[id] = s
		m.currents[source] = id
		return s, nil
	}
	s := New(source)
	if err := m.store.SaveSession(s); err != nil {
		return nil, err
	}
	if err := m.store.SetCurrentSession(source, s.ID); err != nil {
		return nil, err
	}
	m.sessions[s.ID] = s
	m.currents[source] = s.ID
	return s, nil
}

func (m *Manager) New(source string) (*Session, error) {
	source = normalizeSource(source)
	s := New(source)
	if err := m.store.SaveSession(s); err != nil {
		return nil, err
	}
	if err := m.store.SetCurrentSession(source, s.ID); err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.sessions[s.ID] = s
	m.currents[source] = s.ID
	m.mu.Unlock()
	return s, nil
}

func (m *Manager) Save(s *Session) error {
	if s == nil {
		return fmt.Errorf("session is nil")
	}
	if err := m.store.SaveSession(s); err != nil {
		return err
	}
	m.mu.Lock()
	m.sessions[s.ID] = s
	m.mu.Unlock()
	return nil
}

func (m *Manager) Switch(source, id string) (*Session, error) {
	source = normalizeSource(source)
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("session id is required")
	}
	s, err := m.store.LoadSession(id)
	if err != nil {
		return nil, err
	}
	if normalizeSource(s.Source) != source {
		return nil, fmt.Errorf("session %s belongs to source %s, not %s", id, normalizeSource(s.Source), source)
	}
	if err := m.store.SetCurrentSession(source, id); err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.sessions[id] = s
	m.currents[source] = id
	m.mu.Unlock()
	return s, nil
}

func (m *Manager) List() ([]*Session, error) {
	sessions, err := m.store.ListSessions()
	if err != nil {
		return nil, err
	}
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})
	return sessions, nil
}

func (m *Manager) ListBySource(source string) ([]*Session, error) {
	source = normalizeSource(source)
	sessions, err := m.List()
	if err != nil {
		return nil, err
	}
	out := make([]*Session, 0, len(sessions))
	for _, s := range sessions {
		if normalizeSource(s.Source) == source {
			out = append(out, s)
		}
	}
	return out, nil
}

func normalizeSource(source string) string {
	source = strings.TrimSpace(source)
	if source == "" {
		return "default"
	}
	return source
}
