package session

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/abcdlsj/mink/internal/xstr"
	"github.com/abcdlsj/mink/msg"
)

type Session struct {
	id        string
	kind      string
	status    string
	summary   string
	meta      json.RawMessage
	closedAt  *time.Time
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

func (s *Session) View() View {
	msgs, anchor := buildView(s, -1)
	return View{Messages: msgs, Anchor: anchor}
}

var firstNonEmptySnapshot = xstr.NonEmpty

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
