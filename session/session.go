package session

import (
	"crypto/sha1"
	"encoding/hex"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/textutil"
)

type Session struct {
	ID              string
	Source          string
	Title           string
	Summary         string
	Messages        []msg.Message
	CreatedAt       time.Time
	UpdatedAt       time.Time
	ExternalSession map[string]string
}

func New(source string) *Session {
	now := time.Now()
	return &Session{
		ID:              newID(source, now),
		Source:          strings.TrimSpace(source),
		CreatedAt:       now,
		UpdatedAt:       now,
		ExternalSession: make(map[string]string),
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

func (s *Session) Empty() bool {
	if s == nil {
		return true
	}
	if strings.TrimSpace(s.Summary) != "" {
		return false
	}
	return len(s.Messages) == 0
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
	return textutil.Preview(strings.ReplaceAll(s, "\n", " "), 48)
}

func newID(source string, now time.Time) string {
	src := sourceTag(source)
	sum := sha1.Sum([]byte(source + now.UTC().Format(time.RFC3339Nano) + uuid.NewString()))
	return now.Format("20060102") + "-" + src + "-" + hex.EncodeToString(sum[:4])
}

func sourceTag(source string) string {
	source = strings.TrimSpace(strings.ToLower(source))
	if source == "" {
		return "default"
	}
	if i := strings.IndexByte(source, ':'); i >= 0 {
		source = source[:i]
	}
	var b strings.Builder
	lastDash := false
	for _, r := range source {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case !lastDash:
			b.WriteByte('-')
			lastDash = true
		}
		if b.Len() >= 12 {
			break
		}
	}
	tag := strings.Trim(b.String(), "-")
	if tag == "" {
		return "default"
	}
	return tag
}
