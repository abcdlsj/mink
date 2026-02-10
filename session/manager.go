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

// Message 会话消息
type Message struct {
	ID               string                 `json:"id"`
	Role             string                 `json:"role"`
	Content          string                 `json:"content"`
	ReasoningContent string                 `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCall             `json:"tool_calls,omitempty"`
	ToolResults      []ToolResult           `json:"tool_results,omitempty"`
	CustomData       map[string]interface{} `json:"custom_data,omitempty"`
	Timestamp        time.Time              `json:"timestamp"`
	ParentID         string                 `json:"parent_id,omitempty"`
	BranchName       string                 `json:"branch_name,omitempty"`
}

type ToolCall struct {
	ID   string          `json:"id"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

type ToolResult struct {
	ToolCallID string `json:"tool_call_id"`
	Content    string `json:"content"`
	Error      string `json:"error,omitempty"`
}

// Session 会话
type Session struct {
	ID          string    `json:"id"`
	RootID      string    `json:"root_id"`
	ParentID    string    `json:"parent_id"`
	Name        string    `json:"name"`
	Messages    []Message `json:"messages"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	IsCompacted bool      `json:"is_compacted"`
	Summary     string    `json:"summary,omitempty"`
}

// Manager 会话管理器
type Manager struct {
	sessionsDir string
	activeID    string
}

func NewManager(sessionsDir string) *Manager {
	os.MkdirAll(sessionsDir, 0755)
	return &Manager{sessionsDir: sessionsDir}
}

// Create 创建新会话
func (m *Manager) Create() (*Session, error) {
	id := uuid.New().String()[:8]
	session := &Session{
		ID:        id,
		RootID:    id,
		Name:      "main",
		Messages:  []Message{},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := m.Save(session); err != nil {
		return nil, err
	}
	m.activeID = id
	return session, nil
}

// Load 加载会话
func (m *Manager) Load(id string) (*Session, error) {
	path := filepath.Join(m.sessionsDir, id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var session Session
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, err
	}
	return &session, nil
}

// Save 保存会话
func (m *Manager) Save(s *Session) error {
	s.UpdatedAt = time.Now()
	path := filepath.Join(m.sessionsDir, s.ID+".json")
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// Branch 创建分支
func (m *Manager) Branch(parentID string, name string) (*Session, error) {
	parent, err := m.Load(parentID)
	if err != nil {
		return nil, err
	}

	id := uuid.New().String()[:8]
	messages := make([]Message, len(parent.Messages))
	copy(messages, parent.Messages)

	messages = append(messages, Message{
		ID:         uuid.New().String()[:8],
		Role:       "system",
		Content:    fmt.Sprintf("Branch created: %s from %s", name, parentID),
		Timestamp:  time.Now(),
		ParentID:   parentID,
		BranchName: name,
	})

	session := &Session{
		ID:        id,
		RootID:    parent.RootID,
		ParentID:  parentID,
		Name:      name,
		Messages:  messages,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := m.Save(session); err != nil {
		return nil, err
	}
	return session, nil
}

// Compact 压缩会话
func (m *Manager) Compact(sessionID string, summary string) error {
	session, err := m.Load(sessionID)
	if err != nil {
		return err
	}

	var recent []Message
	for i := len(session.Messages) - 1; i >= 0 && len(recent) < 6; i-- {
		recent = append(recent, session.Messages[i])
	}

	var compacted []Message
	for i := len(recent) - 1; i >= 0; i-- {
		compacted = append(compacted, recent[i])
	}

	summaryMsg := Message{
		ID:        uuid.New().String()[:8],
		Role:      "system",
		Content:   "[Previous conversation summary]\n" + summary,
		Timestamp: time.Now(),
	}
	compacted = append([]Message{summaryMsg}, compacted...)

	session.Messages = compacted
	session.IsCompacted = true
	session.Summary = summary

	return m.Save(session)
}

// AddMessage 添加消息
func (m *Manager) AddMessage(sessionID string, msg Message) error {
	session, err := m.Load(sessionID)
	if err != nil {
		return err
	}
	if msg.ID == "" {
		msg.ID = uuid.New().String()[:8]
	}
	if msg.Timestamp.IsZero() {
		msg.Timestamp = time.Now()
	}
	session.Messages = append(session.Messages, msg)
	return m.Save(session)
}

// GetHistory 获取历史
func (m *Manager) GetHistory(sessionID string, limit int) ([]Message, error) {
	session, err := m.Load(sessionID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > len(session.Messages) {
		return session.Messages, nil
	}
	return session.Messages[len(session.Messages)-limit:], nil
}

// List 列出所有会话
func (m *Manager) List() ([]*Session, error) {
	entries, err := os.ReadDir(m.sessionsDir)
	if err != nil {
		return nil, err
	}

	var sessions []*Session
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		session, err := m.Load(strings.TrimSuffix(entry.Name(), ".json"))
		if err != nil {
			continue
		}
		sessions = append(sessions, session)
	}
	return sessions, nil
}

// Active 获取当前活跃会话
func (m *Manager) Active() (*Session, error) {
	if m.activeID == "" {
		return nil, fmt.Errorf("no active session")
	}
	return m.Load(m.activeID)
}

// SetActive 设置活跃会话
func (m *Manager) SetActive(id string) {
	m.activeID = id
}
