package session

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/abcdlsj/mink/msg"
)

type Session struct {
	id        string
	kind      string
	status    string
	summary   string
	meta      json.RawMessage
	closedAt  *time.Time
	store     *SQLiteStore
	entries   []Entry
	anchors   []Anchor
	prov      *Provenance
	createdAt time.Time
	updatedAt time.Time
	dirty     bool
	resolve   func(string) (*Session, error)
	mu        sync.RWMutex
}

func newSession(id string, store *SQLiteStore, resolve func(string) (*Session, error)) *Session {
	now := time.Now()
	return &Session{
		id:        id,
		kind:      "main",
		status:    "active",
		meta:      json.RawMessage("{}"),
		store:     store,
		createdAt: now,
		updatedAt: now,
		resolve:   resolve,
	}
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

func (s *Session) Kind() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.kind
}

func (s *Session) Status() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status
}

func (s *Session) Summary() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.summary
}

func (s *Session) ClosedAt() *time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneTimePtr(s.closedAt)
}

func (s *Session) SetKind(kind string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	kind = firstNonEmptySnapshot(kind, "main")
	if s.kind == kind {
		return
	}
	s.kind = kind
	s.touchLocked()
}

func (s *Session) SetStatus(status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	status = firstNonEmptySnapshot(status, "active")
	if s.status == status {
		return
	}
	s.status = status
	if status == "closed" {
		now := time.Now()
		s.closedAt = &now
	} else {
		s.closedAt = nil
	}
	s.touchLocked()
}

func (s *Session) SetSummary(summary string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	summary = strings.TrimSpace(summary)
	if s.summary == summary {
		return
	}
	s.summary = summary
	s.touchLocked()
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
	if kind == AnchorSummary {
		s.summary = summary
	}
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
	msgs, anchor := buildView(s, -1)
	return View{Messages: msgs, Anchor: anchor}
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
		Kind:       firstNonEmptySnapshot(s.kind, "main"),
		Status:     firstNonEmptySnapshot(s.status, "active"),
		Summary:    s.summary,
		Metadata:   cloneJSON(s.meta),
		CreatedAt:  s.createdAt,
		UpdatedAt:  s.updatedAt,
		ClosedAt:   cloneTimePtr(s.closedAt),
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
	s.kind = firstNonEmptySnapshot(snap.Kind, "main")
	s.status = firstNonEmptySnapshot(snap.Status, "active")
	s.summary = snap.Summary
	s.meta = cloneJSON(snap.Metadata)
	s.closedAt = cloneTimePtr(snap.ClosedAt)
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

func cloneJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("{}")
	}
	cp := make([]byte, len(raw))
	copy(cp, raw)
	return json.RawMessage(cp)
}

func cloneTimePtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	cp := *t
	return &cp
}

func firstNonEmptySnapshot(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
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
