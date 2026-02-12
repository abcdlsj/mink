package session

import (
	"sync"

	"github.com/google/uuid"
)

type Manager struct {
	store    Store
	sessions map[string]*Session
	mu       sync.RWMutex
}

func NewManager(store Store) *Manager {
	return &Manager{
		store:    store,
		sessions: make(map[string]*Session),
	}
}

func (m *Manager) Create() (*Session, error) {
	id := uuid.New().String()[:8]
	s := newSession(id, m.store)

	m.mu.Lock()
	m.sessions[id] = s
	m.mu.Unlock()

	return s, nil
}

func (m *Manager) Get(id string) (*Session, error) {
	m.mu.RLock()
	if s, ok := m.sessions[id]; ok {
		m.mu.RUnlock()
		return s, nil
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()

	if s, ok := m.sessions[id]; ok {
		return s, nil
	}

	s := newSession(id, m.store)
	if err := s.load(); err != nil {
		return nil, err
	}
	m.sessions[id] = s
	return s, nil
}

func (m *Manager) List() ([]string, error) {
	return m.store.List()
}

func (m *Manager) FlushAll() error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, s := range m.sessions {
		if err := s.Flush(); err != nil {
			return err
		}
	}
	return nil
}
