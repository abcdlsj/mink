package session

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
)

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

func (m *Manager) Fork(parent *Session) (*Session, error) {
	if parent == nil {
		return m.Create()
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	child := m.createLocked()
	child.kind = firstNonEmptySnapshot(parent.Kind(), "main")
	child.status = firstNonEmptySnapshot(parent.Status(), "active")

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
