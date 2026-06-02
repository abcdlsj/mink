package space

import (
	"crypto/sha1"
	"encoding/hex"
	"strings"
	"time"

	"github.com/google/uuid"
)

func NewID(kind Kind, seed string, now time.Time) string {
	tag := strings.TrimSpace(strings.ToLower(string(kind)))
	if tag == "" {
		tag = "space"
	}
	if i := strings.IndexByte(tag, '_'); i >= 0 {
		tag = tag[:i]
	}
	if len(tag) > 7 {
		tag = tag[:7]
	}
	sum := sha1.Sum([]byte(string(kind) + ":" + seed + ":" + now.UTC().Format(time.RFC3339Nano) + ":" + uuid.NewString()))
	return now.Format("20060102") + "-" + tag + "-" + hex.EncodeToString(sum[:4])
}

func New(kind Kind, title string, participants []Participant) *Space {
	now := time.Now()
	return &Space{
		ID:           NewID(kind, title, now),
		Kind:         kind,
		Title:        strings.TrimSpace(title),
		Participants: append([]Participant(nil), participants...),
		Messages:     nil,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}

func (s *Space) HasParticipant(id string) bool {
	for _, p := range s.Participants {
		if p.ID == id {
			return true
		}
	}
	return false
}

func (s *Space) AddParticipant(p Participant) bool {
	if s.HasParticipant(p.ID) {
		return false
	}
	if p.JoinedAt.IsZero() {
		p.JoinedAt = time.Now()
	}
	if p.Status == "" {
		p.Status = StatusAvailable
	}
	s.Participants = append(s.Participants, p)
	s.UpdatedAt = p.JoinedAt
	return true
}

func (s *Space) AddMessage(m Message) Message {
	if m.ID == "" {
		m.ID = uuid.New().String()[:8]
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now()
	}
	m.SpaceID = s.ID
	s.Messages = append(s.Messages, m)
	s.UpdatedAt = m.CreatedAt
	return m
}

func (s *Space) Replies(parentID string) []Message {
	if parentID == "" {
		return nil
	}
	out := make([]Message, 0)
	for _, m := range s.Messages {
		if m.ParentMessageID == parentID {
			out = append(out, m)
		}
	}
	return out
}

func IsSpaceID(s string) bool {
	if len(s) < 9 {
		return false
	}
	for i := 0; i < 8; i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return false
		}
	}
	return s[8] == '-'
}

func AgentParticipantID(sp *Space) string {
	if sp == nil {
		return ""
	}
	for _, p := range sp.Participants {
		if p.Kind == ParticipantAgent {
			return p.ID
		}
	}
	return ""
}
