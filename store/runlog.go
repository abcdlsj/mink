package store

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"github.com/abcdlsj/sumi/bus"
)

func (s *Store) AppendEvent(ev bus.Event) error {
	line, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var wrote bool
	if strings.TrimSpace(ev.SessionID) != "" {
		if err := appendLine(s.runlogPath(ev.SessionID, ev.Source, ev.Time), line); err != nil {
			return err
		}
		wrote = true
	}
	if strings.TrimSpace(ev.TaskID) != "" {
		if err := appendLine(s.taskRunlogPath(ev.TaskID), line); err != nil {
			return err
		}
		wrote = true
	}
	if wrote {
		return nil
	}
	return appendLine(s.globalRunlog, line)
}

func (s *Store) ReplaySession(id string, limit int) ([]bus.Event, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path, ok, err := s.findRunlogPathLocked(id)
	if err != nil {
		return nil, err
	}
	if ok {
		return s.replayPathLocked(path, limit, nil)
	}
	return nil, nil
}

func (s *Store) ReplayTask(id string, limit int) ([]bus.Event, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.taskRunlogPath(id)
	if fileExists(path) {
		return s.replayPathLocked(path, limit, nil)
	}
	return nil, nil
}

func (s *Store) replayPathLocked(path string, limit int, keep func(bus.Event) bool) ([]bus.Event, error) {
	var out []bus.Event
	err := scanJSONLines(path, func(ev bus.Event) error {
		if keep != nil && !keep(ev) {
			return nil
		}
		out = append(out, ev)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

func (s *Store) findRunlogPathLocked(id string) (string, bool, error) {
	if date, tag, ok := parseSessionID(id); ok {
		path := filepath.Join(s.runlogDir, tag, date, id+".jsonl")
		if fileExists(path) {
			return path, true, nil
		}
	}
	return "", false, nil
}

func (s *Store) appendSessionOpLocked(typ, source, id string, created time.Time, path string) error {
	ev := bus.Event{
		Type:      typ,
		Source:    strings.TrimSpace(source),
		SessionID: strings.TrimSpace(id),
		Output:    strings.TrimSpace(path),
		Time:      time.Now(),
	}
	line, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	if ev.SessionID != "" {
		if err := appendLine(s.runlogPath(ev.SessionID, ev.Source, created), line); err != nil {
			return err
		}
	}
	return appendLine(s.globalRunlog, line)
}

func (s *Store) currentFromRunlogLocked(source string) (string, error) {
	source = strings.TrimSpace(source)
	var id string
	if fileExists(s.globalRunlog) {
		if err := scanCurrentSession(s.globalRunlog, source, &id); err != nil {
			return "", err
		}
	}
	return id, nil
}

func scanCurrentSession(path, source string, id *string) error {
	return scanJSONLines(path, func(ev bus.Event) error {
		if ev.Type == bus.SessionSwitched && strings.TrimSpace(ev.Source) == source {
			*id = strings.TrimSpace(ev.SessionID)
		}
		return nil
	})
}
