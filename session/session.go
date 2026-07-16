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
	ID      string
	Source  string
	Title   string
	Summary string
	// Messages is a disposable runtime context cache. It may be compacted,
	// rebuilt from Space, or deleted; do not use it as product-visible chat
	// history.
	Messages        []msg.Message
	Usage           Usage
	CreatedAt       time.Time
	UpdatedAt       time.Time
	ExternalSession map[string]string
	// Checkpoint records which prefix of the source Space history has been
	// compacted into a persistent summary, so later projection rounds can
	// rebuild the runtime context as [summary] + un-compacted Space suffix
	// instead of re-loading (and re-compacting) the full history every turn.
	// It is nil until an overflow compact happens for this session.
	Checkpoint *ProjectionCheckpoint
}

// ProjectionCheckpoint is the persistent record of a compact boundary. The
// truth source is always the Space; this only declares "history up to
// SummaryThroughMessageID has been folded into Summary". Profile is a neutral
// string (not app.ContextProfile) to keep the session package free of a
// reverse dependency on app.
type ProjectionCheckpoint struct {
	SpaceID                 string `json:"space_id"`
	ParentMessageID         string `json:"parent_message_id,omitempty"`
	AgentID                 string `json:"agent_id,omitempty"`
	Profile                 string `json:"profile,omitempty"`
	SummaryThroughMessageID string `json:"summary_through_message_id"`
	Summary                 string `json:"summary"`
	PrefixFingerprint       string `json:"prefix_fingerprint"`
}

// Clone returns a deep copy so callers can persist a stable snapshot.
func (c *ProjectionCheckpoint) Clone() *ProjectionCheckpoint {
	if c == nil {
		return nil
	}
	cp := *c
	return &cp
}

type Usage struct {
	Calls   int           `json:"calls"`
	Input   int           `json:"input"`
	Output  int           `json:"output"`
	Total   int           `json:"total"`
	Records []UsageRecord `json:"records,omitempty"`
}

type UsageRecord struct {
	MessageID string    `json:"message_id,omitempty"`
	Input     int       `json:"input"`
	Output    int       `json:"output"`
	Total     int       `json:"total"`
	Source    string    `json:"source,omitempty"`
	At        time.Time `json:"at"`
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
	s.addUsage(m.ID, m.Usage, m.Timestamp)
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

func (s *Session) addUsage(id string, u *msg.TokenUsage, at time.Time) {
	if s == nil || u == nil {
		return
	}
	if u.Input == 0 && u.Output == 0 && u.Total == 0 {
		return
	}
	id = strings.TrimSpace(id)
	if id != "" && s.hasUsage(id) {
		return
	}
	if at.IsZero() {
		at = time.Now()
	}
	total := u.Total
	if total == 0 {
		total = u.Input + u.Output
	}
	s.Usage.Calls++
	s.Usage.Input += u.Input
	s.Usage.Output += u.Output
	s.Usage.Total += total
	s.Usage.Records = append(s.Usage.Records, UsageRecord{
		MessageID: id,
		Input:     u.Input,
		Output:    u.Output,
		Total:     total,
		Source:    strings.TrimSpace(u.Source),
		At:        at,
	})
}

func (s *Session) hasUsage(id string) bool {
	for _, r := range s.Usage.Records {
		if r.MessageID == id {
			return true
		}
	}
	return false
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
