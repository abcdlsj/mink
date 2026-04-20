package store

import (
	"encoding/json"
	"strings"

	"github.com/abcdlsj/mink/bus"
)

func (s *Store) AppendEvent(ev bus.Event) error {
	line, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return appendLine(s.runlog, line)
}

func (s *Store) ReplaySession(id string, limit int) ([]bus.Event, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, nil
	}
	return s.replay(limit, func(ev bus.Event) bool { return ev.SessionID == id })
}

func (s *Store) ReplayTask(id string, limit int) ([]bus.Event, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, nil
	}
	return s.replay(limit, func(ev bus.Event) bool { return ev.TaskID == id })
}

func (s *Store) replay(limit int, keep func(bus.Event) bool) ([]bus.Event, error) {
	evs, err := s.loadEvents(keep)
	if err != nil {
		return nil, err
	}
	if limit > 0 && len(evs) > limit {
		evs = evs[len(evs)-limit:]
	}
	return evs, nil
}

func (s *Store) loadEvents(keep func(bus.Event) bool) ([]bus.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []bus.Event
	err := scanJSONLines[bus.Event](s.runlog, func(ev bus.Event) error {
		if keep != nil && !keep(ev) {
			return nil
		}
		out = append(out, ev)
		return nil
	})
	return out, err
}
