package session

import (
	"os"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/abcdlsj/mink/msg"
)

type Session struct {
	id        string
	store     Store
	entries   []Entry
	anchors   []Anchor
	prov      *Provenance
	createdAt time.Time
	updatedAt time.Time
	dirty     bool
	resolve   func(string) (*Session, error)
	mu        sync.RWMutex
}

func newSession(id string, store Store, resolve func(string) (*Session, error)) *Session {
	now := time.Now()
	return &Session{id: id, store: store, createdAt: now, updatedAt: now, resolve: resolve}
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
	s.entries = append(s.entries, Entry{
		ID:        m.ID,
		Kind:      entryKind(m),
		Message:   m,
		CreatedAt: m.Timestamp,
	})
	s.touchLocked()
}

func (s *Session) Messages() []msg.Message {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return entriesToMessages(s.entries)
}

func (s *Session) Entries() []Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r := make([]Entry, len(s.entries))
	copy(r, s.entries)
	return r
}

func (s *Session) EntryCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.entries)
}

func (s *Session) Anchors() []Anchor {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r := make([]Anchor, len(s.anchors))
	copy(r, s.anchors)
	return r
}

func (s *Session) LatestAnchor() *Anchor {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.anchors) == 0 {
		return nil
	}
	a := s.anchors[len(s.anchors)-1]
	return &a
}

func (s *Session) Provenance() *Provenance {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.prov == nil {
		return nil
	}
	p := *s.prov
	return &p
}

func (s *Session) AddAnchor(kind AnchorKind, summary, note string, entryCount int) Anchor {
	s.mu.Lock()
	defer s.mu.Unlock()
	if entryCount < 0 {
		entryCount = 0
	}
	if entryCount > len(s.entries) {
		entryCount = len(s.entries)
	}
	a := Anchor{
		ID:         uuid.New().String()[:8],
		Kind:       kind,
		Summary:    summary,
		Note:       note,
		EntryCount: entryCount,
		CreatedAt:  time.Now(),
	}
	s.anchors = append(s.anchors, a)
	s.touchLocked()
	return a
}

func (s *Session) SetProvenance(p Provenance) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prov = &p
	s.touchLocked()
}

func (s *Session) View() View {
	return NewViewBuilder().Build(s)
}

func (s *Session) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dirty {
		return nil
	}
	if err := s.store.Save(s.id, s.snapshotLocked()); err != nil {
		return err
	}
	s.dirty = false
	return nil
}

func (s *Session) load() error {
	snap, err := s.store.Load(s.id)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	s.applySnapshot(snap)
	return nil
}

func (s *Session) snapshot() *Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshotLocked()
}

func (s *Session) snapshotLocked() *Snapshot {
	entries := make([]Entry, len(s.entries))
	copy(entries, s.entries)
	anchors := make([]Anchor, len(s.anchors))
	copy(anchors, s.anchors)
	var prov *Provenance
	if s.prov != nil {
		p := *s.prov
		prov = &p
	}
	return &Snapshot{
		Version:    1,
		ID:         s.id,
		CreatedAt:  s.createdAt,
		UpdatedAt:  s.updatedAt,
		Entries:    entries,
		Anchors:    anchors,
		Provenance: prov,
	}
}

func (s *Session) applySnapshot(snap *Snapshot) {
	if snap == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.id = snap.ID
	s.createdAt = snap.CreatedAt
	if s.createdAt.IsZero() {
		s.createdAt = time.Now()
	}
	s.updatedAt = snap.UpdatedAt
	if s.updatedAt.IsZero() {
		s.updatedAt = s.createdAt
	}
	s.entries = append(s.entries[:0], snap.Entries...)
	s.anchors = append(s.anchors[:0], snap.Anchors...)
	if snap.Provenance != nil {
		p := *snap.Provenance
		s.prov = &p
	} else {
		s.prov = nil
	}
	s.dirty = false
}

func (s *Session) parent(id string) (*Session, error) {
	if id == "" || s.resolve == nil {
		return nil, nil
	}
	return s.resolve(id)
}

func (s *Session) touchLocked() {
	if s.createdAt.IsZero() {
		s.createdAt = time.Now()
	}
	s.updatedAt = time.Now()
	s.dirty = true
}

func entryKind(m msg.Message) EntryKind {
	switch m.Role {
	case "user":
		return EntryUser
	case "assistant":
		return EntryAssistant
	case "tool":
		return EntryTool
	case "system":
		return EntrySystem
	default:
		return EntryNote
	}
}
