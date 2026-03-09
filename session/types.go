package session

import (
	"time"

	"github.com/abcdlsj/mink/msg"
)

type EntryKind string

const (
	EntryUser      EntryKind = "user"
	EntryAssistant EntryKind = "assistant"
	EntryTool      EntryKind = "tool"
	EntrySystem    EntryKind = "system"
	EntryNote      EntryKind = "note"
)

type AnchorKind string

const (
	AnchorSummary AnchorKind = "summary"
)

type Entry struct {
	ID        string      `json:"id"`
	Kind      EntryKind   `json:"kind"`
	Message   msg.Message `json:"message"`
	CreatedAt time.Time   `json:"created_at"`
}

type Anchor struct {
	ID         string     `json:"id"`
	Kind       AnchorKind `json:"kind"`
	Summary    string     `json:"summary"`
	Note       string     `json:"note,omitempty"`
	EntryCount int        `json:"entry_count"`
	CreatedAt  time.Time  `json:"created_at"`
}

type Provenance struct {
	ParentSessionID string    `json:"parent_session_id"`
	ForkEntryCount  int       `json:"fork_entry_count"`
	ForkAnchorID    string    `json:"fork_anchor_id,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

type Snapshot struct {
	Version    int         `json:"version"`
	ID         string      `json:"id"`
	CreatedAt  time.Time   `json:"created_at"`
	UpdatedAt  time.Time   `json:"updated_at"`
	Entries    []Entry     `json:"entries,omitempty"`
	Anchors    []Anchor    `json:"anchors,omitempty"`
	Provenance *Provenance `json:"provenance,omitempty"`
}

type View struct {
	Messages []msg.Message
	Anchor   *Anchor
}
