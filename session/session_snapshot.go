package session

import (
	"encoding/json"
	"os"
	"time"
)

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
