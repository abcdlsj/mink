package session

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

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

func (s *Session) MetaString(key string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if key == "" || len(s.meta) == 0 {
		return ""
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(s.meta, &data); err != nil {
		return ""
	}
	raw, ok := data[key]
	if !ok || len(raw) == 0 {
		return ""
	}
	var v string
	if err := json.Unmarshal(raw, &v); err != nil {
		return ""
	}
	return strings.TrimSpace(v)
}

func (s *Session) SetMetaString(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if key == "" {
		return nil
	}
	value = strings.TrimSpace(value)
	data := map[string]json.RawMessage{}
	if len(s.meta) > 0 && json.Valid(s.meta) {
		if err := json.Unmarshal(s.meta, &data); err != nil {
			return err
		}
	}
	if value == "" {
		if _, ok := data[key]; !ok {
			return nil
		}
		delete(data, key)
	} else {
		raw, err := json.Marshal(value)
		if err != nil {
			return err
		}
		if cur, ok := data[key]; ok && string(cur) == string(raw) {
			return nil
		}
		data[key] = raw
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("session metadata: %w", err)
	}
	s.meta = json.RawMessage(raw)
	s.touchLocked()
	return nil
}

func (s *Session) SetProvenance(p Provenance) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prov = &p
	s.touchLocked()
}
