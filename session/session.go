package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Message struct {
	ID          string                 `json:"id"`
	Role        string                 `json:"role"`
	Content     string                 `json:"content"`
	ToolCalls   []ToolCall             `json:"tc,omitempty"`
	ToolResults []ToolResult           `json:"tr,omitempty"`
	CustomData  map[string]interface{} `json:"data,omitempty"`
	Time        time.Time              `json:"t"`
	ParentID    string                 `json:"pid,omitempty"`
	BranchName  string                 `json:"branch,omitempty"`
}

type ToolCall struct {
	ID   string          `json:"id"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

type ToolResult struct {
	ToolCallID string `json:"tcid"`
	Content    string `json:"content"`
	Error      string `json:"err,omitempty"`
}

type Session struct {
	ID       string    `json:"id"`
	RootID   string    `json:"root"`
	ParentID string    `json:"pid,omitempty"`
	Name     string    `json:"name"`
	Msgs     []Message `json:"msgs"`
	Created  time.Time `json:"created"`
	Updated  time.Time `json:"updated"`
	Compact  bool      `json:"compact"`
	Summary  string    `json:"sum,omitempty"`
}

type Manager struct {
	dir string
	sid string
}

func NewManager(dir string) *Manager {
	os.MkdirAll(dir, 0755)
	return &Manager{dir: dir}
}

func (m *Manager) Create() (*Session, error) {
	id := uuid.New().String()[:8]
	s := &Session{
		ID:      id,
		RootID:  id,
		Name:    "main",
		Created: time.Now(),
		Updated: time.Now(),
	}
	return s, m.save(s)
}

func (m *Manager) Load(id string) (*Session, error) {
	b, err := os.ReadFile(filepath.Join(m.dir, id+".json"))
	if err != nil {
		return nil, err
	}
	var s Session
	return &s, json.Unmarshal(b, &s)
}

func (m *Manager) save(s *Session) error {
	s.Updated = time.Now()
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(m.dir, s.ID+".json"), b, 0644)
}

func (m *Manager) Branch(pid, name string) (*Session, error) {
	p, err := m.Load(pid)
	if err != nil {
		return nil, err
	}

	id := uuid.New().String()[:8]
	msgs := make([]Message, len(p.Msgs))
	copy(msgs, p.Msgs)
	msgs = append(msgs, Message{
		ID:         uuid.New().String()[:8],
		Role:       "system",
		Content:    fmt.Sprintf("branch: %s from %s", name, pid),
		Time:       time.Now(),
		ParentID:   pid,
		BranchName: name,
	})

	s := &Session{
		ID:      id,
		RootID:  p.RootID,
		ParentID: pid,
		Name:    name,
		Msgs:    msgs,
		Created: time.Now(),
		Updated: time.Now(),
	}
	return s, m.save(s)
}

func (m *Manager) Compact(id, sum string) error {
	s, err := m.Load(id)
	if err != nil {
		return err
	}

	var recent []Message
	for i := len(s.Msgs) - 1; i >= 0 && len(recent) < 6; i-- {
		recent = append(recent, s.Msgs[i])
	}

	var compacted []Message
	for i := len(recent) - 1; i >= 0; i-- {
		compacted = append(compacted, recent[i])
	}

	compacted = append([]Message{{
		ID:      uuid.New().String()[:8],
		Role:    "system",
		Content: "[summary]\n" + sum,
		Time:    time.Now(),
	}}, compacted...)

	s.Msgs = compacted
	s.Compact = true
	s.Summary = sum
	return m.save(s)
}

func (m *Manager) AddMessage(id string, msg Message) error {
	s, err := m.Load(id)
	if err != nil {
		return err
	}
	if msg.ID == "" {
		msg.ID = uuid.New().String()[:8]
	}
	if msg.Time.IsZero() {
		msg.Time = time.Now()
	}
	s.Msgs = append(s.Msgs, msg)
	return m.save(s)
}

func (m *Manager) GetHistory(id string, limit int) ([]Message, error) {
	s, err := m.Load(id)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > len(s.Msgs) {
		return s.Msgs, nil
	}
	return s.Msgs[len(s.Msgs)-limit:], nil
}

func (m *Manager) List() ([]*Session, error) {
	ents, err := os.ReadDir(m.dir)
	if err != nil {
		return nil, err
	}

	var ss []*Session
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		s, err := m.Load(strings.TrimSuffix(e.Name(), ".json"))
		if err != nil {
			continue
		}
		ss = append(ss, s)
	}
	return ss, nil
}

func (m *Manager) Active() (*Session, error) {
	if m.sid == "" {
		return nil, fmt.Errorf("no session")
	}
	return m.Load(m.sid)
}

func (m *Manager) SetActive(id string) {
	m.sid = id
}
