package store

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/abcdlsj/mink/msg"
	"github.com/abcdlsj/mink/session"
)

type diskSession struct {
	ID        string        `json:"id"`
	Source    string        `json:"source"`
	Title     string        `json:"title,omitempty"`
	Summary   string        `json:"summary,omitempty"`
	Messages  []msg.Message `json:"messages,omitempty"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
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
	return appendLine(s.path, line)
}

func (s *Store) LoadSession(id string) (*session.Session, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("session not found: %s", id)
	}
	m, err := s.loadSessions()
	if err != nil {
		return nil, err
	}
	d, ok := m[id]
	if !ok {
		return nil, fmt.Errorf("session not found: %s", id)
	}
	return fromDisk(d), nil
}

func (s *Store) ListSessions() ([]*session.Session, error) {
	m, err := s.loadSessions()
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
	m, err := s.loadCurrent()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(m[source]), nil
}

func (s *Store) SetCurrentSession(source, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.loadCurrent()
	if err != nil {
		return err
	}
	m[source] = strings.TrimSpace(id)
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(s.current, append(data, '\n'))
}

func (s *Store) loadSessions() (map[string]diskSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]diskSession{}
	err := scanJSONLines(s.path, func(d diskSession) error {
		out[d.ID] = d
		return nil
	})
	return out, err
}

func (s *Store) loadCurrent() (map[string]string, error) {
	data, err := os.ReadFile(s.current)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return map[string]string{}, nil
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	if m == nil {
		m = map[string]string{}
	}
	return m, nil
}

func toDisk(s *session.Session) diskSession {
	return diskSession{
		ID:        s.ID,
		Source:    s.Source,
		Title:     s.Title,
		Summary:   s.Summary,
		Messages:  s.Messages,
		CreatedAt: s.CreatedAt,
		UpdatedAt: s.UpdatedAt,
	}
}

func fromDisk(d diskSession) *session.Session {
	return &session.Session{
		ID:        d.ID,
		Source:    d.Source,
		Title:     d.Title,
		Summary:   d.Summary,
		Messages:  d.Messages,
		CreatedAt: d.CreatedAt,
		UpdatedAt: d.UpdatedAt,
	}
}

func appendLine(path string, line []byte) error {
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
