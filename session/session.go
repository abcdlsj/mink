package session

import (
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/abcdlsj/mink/msg"
)

type Session struct {
	ID        string
	Source    string
	Title     string
	Summary   string
	Messages  []msg.Message
	CreatedAt time.Time
	UpdatedAt time.Time
}

func New(source string) *Session {
	now := time.Now()
	return &Session{
		ID:        uuid.New().String()[:8],
		Source:    strings.TrimSpace(source),
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func (s *Session) Add(m msg.Message) {
	if m.ID == "" {
		m.ID = uuid.New().String()[:8]
	}
	if m.Timestamp.IsZero() {
		m.Timestamp = time.Now()
	}
	s.Messages = append(s.Messages, m)
	s.UpdatedAt = m.Timestamp
	if s.CreatedAt.IsZero() {
		s.CreatedAt = m.Timestamp
	}
	if s.Title == "" && m.Role == "user" && m.Content != "" {
		s.Title = trimTitle(m.Content)
	}
}

func (s *Session) Compact(summary string, keep int) {
	if keep < 0 {
		keep = 8
	}
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return
	}
	s.Summary = summary
	if len(s.Messages) > keep {
		msgs := append([]msg.Message{{
			ID:        uuid.New().String()[:8],
			Role:      "system",
			Content:   "[Context Summary]\n" + summary,
			Timestamp: time.Now(),
		}}, s.Messages[len(s.Messages)-keep:]...)
		s.Messages = msgs
	}
	s.UpdatedAt = time.Now()
}

func trimTitle(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) > 48 {
		return s[:48] + "..."
	}
	return s
}
