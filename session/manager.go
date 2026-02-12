package session

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/abcdlsj/mink/bus"
)

type Manager struct {
	store    Store
	sessions map[string]*Session
	bus      *bus.Bus
	mu       sync.RWMutex
}

func NewManager(store Store, b *bus.Bus) *Manager {
	return &Manager{
		store:    store,
		sessions: make(map[string]*Session),
		bus:      b,
	}
}

func generateSessionID() string {
	timestamp := time.Now().Format("20060102150405.000")
	hash := sha256.Sum256([]byte(timestamp + fmt.Sprintf("%d", time.Now().UnixNano())))
	hashSuffix := hex.EncodeToString(hash[:])[:6]
	return fmt.Sprintf("%s_%s", timestamp, hashSuffix)
}

func (m *Manager) Create() (*Session, error) {
	id := generateSessionID()
	s := newSession(id, m.store)

	m.mu.Lock()
	m.sessions[id] = s
	m.mu.Unlock()

	if m.bus != nil {
		_ = m.bus.Pub(bus.Msg{
			Type:    bus.TypeSessionNew,
			From:    bus.AddrSystemSession,
			To:      bus.AddrBroadcast,
			Payload: id,
		})
	}

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

func (m *Manager) GetOrCreate(id string) (*Session, error) {
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
		s = newSession(id, m.store)
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

func (m *Manager) Delete(id string) error {
	m.mu.Lock()
	delete(m.sessions, id)
	m.mu.Unlock()
	return m.store.Delete(id)
}
