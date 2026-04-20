package store

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/msg"
	"github.com/abcdlsj/mink/session"
)

type Store struct {
	path    string
	current string
	runlog  string
	mu      sync.Mutex
}

type diskSession struct {
	ID        string        `json:"id"`
	Source    string        `json:"source"`
	Title     string        `json:"title,omitempty"`
	Summary   string        `json:"summary,omitempty"`
	Messages  []msg.Message `json:"messages,omitempty"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
}

func Open(root string) (*Store, error) {
	root = strings.TrimSpace(root)
	s := &Store{
		path:    filepath.Join(root, "sessions.jsonl"),
		current: filepath.Join(root, "state", "current_sessions.json"),
		runlog:  filepath.Join(root, "runlog.jsonl"),
	}
	for _, dir := range []string{root, filepath.Dir(s.current)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	if err := touch(s.path); err != nil {
		return nil, err
	}
	if err := touch(s.runlog); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return nil }

func (s *Store) SaveSession(v *session.Session) error {
	if v == nil {
		return fmt.Errorf("session is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	line, err := json.Marshal(toDisk(v))
	if err != nil {
		return err
	}
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func (s *Store) LoadSession(id string) (*session.Session, error) {
	m, err := s.loadSessions()
	if err != nil {
		return nil, err
	}
	d, ok := m[strings.TrimSpace(id)]
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
	f, err := os.Open(s.path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[string]diskSession{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var d diskSession
		if err := json.Unmarshal([]byte(line), &d); err != nil {
			return nil, err
		}
		out[d.ID] = d
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
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

func touch(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	return f.Close()
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	name := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		_ = os.Remove(name)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}

func (s *Store) AppendEvent(ev bus.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	line, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(s.runlog, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func (s *Store) ReplaySession(id string, limit int) ([]bus.Event, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, nil
	}
	evs, err := s.loadEvents(func(ev bus.Event) bool {
		return ev.SessionID == id
	})
	if err != nil {
		return nil, err
	}
	if limit > 0 && len(evs) > limit {
		evs = evs[len(evs)-limit:]
	}
	return evs, nil
}

func (s *Store) ReplayTask(id string, limit int) ([]bus.Event, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, nil
	}
	evs, err := s.loadEvents(func(ev bus.Event) bool {
		return ev.TaskID == id
	})
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
	f, err := os.Open(s.runlog)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []bus.Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev bus.Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			return nil, err
		}
		if keep != nil && !keep(ev) {
			continue
		}
		out = append(out, ev)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
