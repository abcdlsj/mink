package session

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/abcdlsj/mink/bus"
)

type Manager struct {
	store    Store
	sessions map[string]*Session
	sources  map[string]string
	bus      *bus.Bus
	mu       sync.RWMutex
}

func NewManager(store Store, b *bus.Bus) *Manager {
	return &Manager{
		store:    store,
		sessions: make(map[string]*Session),
		sources:  make(map[string]string),
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
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.createLocked(), nil
}

func (m *Manager) Current(source string) (*Session, error) {
	if source == "" {
		return m.Create()
	}

	m.mu.RLock()
	if id, ok := m.sources[source]; ok {
		if s, ok := m.sessions[id]; ok {
			m.mu.RUnlock()
			return s, nil
		}
	}
	m.mu.RUnlock()

	m.mu.Lock()
	if id, ok := m.sources[source]; ok {
		if s, ok := m.sessions[id]; ok {
			m.mu.Unlock()
			return s, nil
		}
		s, err := m.loadLocked(id)
		m.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return s, nil
	}

	s := m.createLocked()
	id := s.ID()
	m.sources[source] = id
	m.mu.Unlock()

	m.publishSessionNew(source, id)
	return s, nil
}

func (m *Manager) LookupSource(source string) (*Session, bool) {
	m.mu.RLock()
	id, ok := m.sources[source]
	if !ok {
		m.mu.RUnlock()
		return nil, false
	}
	if s, ok := m.sessions[id]; ok {
		m.mu.RUnlock()
		return s, true
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()

	id, ok = m.sources[source]
	if !ok {
		return nil, false
	}
	if s, ok := m.sessions[id]; ok {
		return s, true
	}
	s, err := m.loadLocked(id)
	if err != nil {
		return nil, false
	}
	return s, true
}

func (m *Manager) RestoreSource(source, id string) error {
	if source == "" || id == "" {
		return nil
	}

	m.mu.Lock()
	if _, err := m.loadLocked(id); err != nil {
		m.mu.Unlock()
		return err
	}
	m.sources[source] = id
	m.mu.Unlock()
	m.publishSessionNew(source, id)
	return nil
}

func (m *Manager) RestoreSources(bindings map[string]string) error {
	for source, id := range bindings {
		if err := m.RestoreSource(source, id); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) ResetSource(source string) (*Session, error) {
	if source == "" {
		return m.Create()
	}

	m.mu.Lock()
	s := m.createLocked()
	id := s.ID()
	m.sources[source] = id
	m.mu.Unlock()

	m.publishSessionNew(source, id)
	return s, nil
}

func (m *Manager) Fork(parent *Session) (*Session, error) {
	if parent == nil {
		return m.Create()
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	child := m.createLocked()
	prov := Provenance{
		ParentSessionID: parent.ID(),
		ForkEntryCount:  parent.EntryCount(),
		CreatedAt:       time.Now(),
	}
	if a := parent.LatestAnchor(); a != nil {
		prov.ForkAnchorID = a.ID
	}
	child.SetProvenance(prov)
	return child, nil
}

func (m *Manager) ForkSource(source string) (*Session, error) {
	if source == "" {
		return m.Create()
	}
	parent, err := m.Current(source)
	if err != nil {
		return nil, err
	}
	child, err := m.Fork(parent)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.sources[source] = child.ID()
	m.mu.Unlock()
	m.publishSessionNew(source, child.ID())
	return child, nil
}

func (m *Manager) CurrentID(source string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	id, ok := m.sources[source]
	return id, ok
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
	return m.loadLocked(id)
}

func (m *Manager) List() ([]string, error) {
	persisted, err := m.store.List()
	if err != nil {
		return nil, err
	}

	m.mu.RLock()
	seen := make(map[string]struct{}, len(persisted)+len(m.sessions))
	ids := make([]string, 0, len(persisted)+len(m.sessions))
	for _, id := range persisted {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	for id := range m.sessions {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	m.mu.RUnlock()

	sort.Strings(ids)
	return ids, nil
}

func (m *Manager) FlushAll() error {
	m.mu.RLock()
	sessions := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		sessions = append(sessions, s)
	}
	m.mu.RUnlock()

	for _, s := range sessions {
		if err := s.Flush(); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) Delete(id string) error {
	m.mu.Lock()
	delete(m.sessions, id)
	for source, sid := range m.sources {
		if sid == id {
			delete(m.sources, source)
		}
	}
	m.mu.Unlock()
	return m.store.Delete(id)
}

func (m *Manager) Bindings() map[string]string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r := make(map[string]string, len(m.sources))
	for source, id := range m.sources {
		r[source] = id
	}
	return r
}

func (m *Manager) createLocked() *Session {
	id := generateSessionID()
	s := newSession(id, m.store, m.Get)
	m.sessions[id] = s
	return s
}

func (m *Manager) loadLocked(id string) (*Session, error) {
	if s, ok := m.sessions[id]; ok {
		return s, nil
	}
	s := newSession(id, m.store, m.Get)
	if err := s.load(); err != nil {
		return nil, err
	}
	m.sessions[id] = s
	return s, nil
}

func (m *Manager) publishSessionNew(source, id string) {
	if m.bus == nil || source == "" || id == "" {
		return
	}
	_ = m.bus.Pub(bus.Msg{
		Type:    bus.TypeSessionNew,
		From:    bus.AddrSystemSession,
		To:      source,
		Payload: id,
	})
}
