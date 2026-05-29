package space

import (
	"crypto/sha1"
	"encoding/hex"
	"strings"
	"time"

	"github.com/google/uuid"
)

// NewID derives a deterministic-ish space identifier from kind, a
// stable seed (e.g. workspace name or agent id), and a wall-clock
// stamp. The shape mirrors session.newID for visual consistency.
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

// New creates a Space populated with the supplied participants.
// Per Iris's amendment, each kind has a minimum participant
// contract that the manager enforces; this constructor is a thin
// helper that does not validate.
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

// HasParticipant reports whether the space already has a member
// with the given id.
func (s *Space) HasParticipant(id string) bool {
	for _, p := range s.Participants {
		if p.ID == id {
			return true
		}
	}
	return false
}

// AddParticipant inserts p when not already present and returns
// true if the membership set changed.
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

// AddMessage appends m to the timeline and updates the space's
// UpdatedAt. It does not validate parent_message_id; the manager
// is responsible for enforcing space-local references.
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

// Replies returns the messages whose ParentMessageID points at
// parentID, in timeline order.
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
