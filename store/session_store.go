package store

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/session"
)

type diskSession struct {
	ID              string            `json:"id"`
	Source          string            `json:"source"`
	Title           string            `json:"title,omitempty"`
	Summary         string            `json:"summary,omitempty"`
	Messages        []msg.Message     `json:"messages,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
	ExternalSession map[string]string `json:"external_session,omitempty"`
}

func (s *Store) SaveSession(v *session.Session) error {
	if v == nil {
		return fmt.Errorf("session is nil")
	}
	line, err := json.Marshal(toDisk(v))
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.sessionPath(v.ID, v.Source, v.CreatedAt)
	typ := sessionOpType(path)
	if err := writeFile(path, append(line, '\n')); err != nil {
		return err
	}
	if err := s.updateIndexLocked(v, path); err != nil {
		return err
	}
	return s.appendSessionOpLocked(typ, v.Source, v.ID, v.CreatedAt, path)
}

func (s *Store) LoadSession(id string) (*session.Session, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("session not found: %s", id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.loadSessionLocked(id)
	if err != nil {
		return nil, err
	}
	return fromDisk(d), nil
}

func (s *Store) ListSessions() ([]*session.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.loadSessionsLocked()
	if err != nil {
		return nil, err
	}
	out := make([]*session.Session, 0, len(m))
	for _, d := range m {
		out = append(out, fromDisk(d))
	}
	return out, nil
}

func (s *Store) CurrentSessionID(source string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id, err := s.currentFromRunlogLocked(source); err != nil {
		return "", err
	} else if id != "" {
		return id, nil
	}
	idx, err := s.loadIndexLocked()
	if err != nil {
		return "", err
	}
	return latestBySource(idx, source), nil
}

func (s *Store) SetCurrentSession(source, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	d, err := s.loadSessionLocked(id)
	if err != nil {
		return err
	}
	return s.appendSessionOpLocked(bus.SessionSwitched, source, id, d.CreatedAt, "")
}

func (s *Store) loadSessionLocked(id string) (diskSession, error) {
	if path, ok, err := s.findSessionPathLocked(id); err != nil {
		return diskSession{}, err
	} else if ok {
		return loadSessionFile(path)
	}
	return diskSession{}, fmt.Errorf("session not found: %s", id)
}

func (s *Store) loadSessionsLocked() (map[string]diskSession, error) {
	out := map[string]diskSession{}
	idx, err := s.loadIndexLocked()
	if err != nil {
		return nil, err
	}
	for id, meta := range idx {
		d, err := loadSessionFile(meta.Path)
		if err != nil {
			return nil, err
		}
		out[d.ID] = d
		if d.ID != id {
			delete(out, id)
		}
	}
	return out, nil
}

func (s *Store) findSessionPathLocked(id string) (string, bool, error) {
	if date, tag, ok := parseSessionID(id); ok {
		path := filepath.Join(s.sessionsDir, tag, date, id+".json")
		if fileExists(path) {
			return path, true, nil
		}
	}
	return "", false, nil
}

func toDisk(s *session.Session) diskSession {
	return diskSession{
		ID:              s.ID,
		Source:          s.Source,
		Title:           s.Title,
		Summary:         s.Summary,
		Messages:        s.Messages,
		CreatedAt:       s.CreatedAt,
		UpdatedAt:       s.UpdatedAt,
		ExternalSession: s.ExternalSession,
	}
}

func fromDisk(d diskSession) *session.Session {
	es := d.ExternalSession
	if es == nil {
		es = make(map[string]string)
	}
	return &session.Session{
		ID:              d.ID,
		Source:          d.Source,
		Title:           d.Title,
		Summary:         d.Summary,
		Messages:        d.Messages,
		CreatedAt:       d.CreatedAt,
		UpdatedAt:       d.UpdatedAt,
		ExternalSession: es,
	}
}

func loadSessionFile(path string) (diskSession, error) {
	data, err := readFile(path)
	if err != nil {
		return diskSession{}, err
	}
	var d diskSession
	if err := json.Unmarshal(data, &d); err != nil {
		return diskSession{}, err
	}
	return d, nil
}

func sessionOpType(path string) string {
	if fileExists(path) {
		return bus.SessionSaved
	}
	return bus.SessionCreated
}

func appendLine(path string, line []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func scanJSONLines[T any](path string, fn func(T) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var v T
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			return err
		}
		if err := fn(v); err != nil {
			return err
		}
	}
	return sc.Err()
}
