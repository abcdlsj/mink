package memory

import (
	"encoding/json"
	"sync"
	"time"
)

type scope struct {
	Kind string
	Key  string
}

type doc struct {
	ID              string
	ScopeKind       string
	ScopeKey        string
	Title           string
	Body            string
	Summary         string
	Kind            string
	Tags            []string
	Source          string
	SourceSpaceID   string
	SourceMessageID string
	CreatedBy       string
	Confidence      string
	ExpiresAt       time.Time
	Path            string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type proposal struct {
	ID              string    `json:"id"`
	ScopeKind       string    `json:"scope_kind"`
	ScopeKey        string    `json:"scope_key"`
	Title           string    `json:"title"`
	Content         string    `json:"content"`
	Kind            string    `json:"kind"`
	Tags            []string  `json:"tags,omitempty"`
	Source          string    `json:"source"`
	SourceSpaceID   string    `json:"source_space_id,omitempty"`
	SourceMessageID string    `json:"source_message_id,omitempty"`
	Reason          string    `json:"reason"`
	Confidence      string    `json:"confidence"`
	CreatedBy       string    `json:"created_by"`
	CreatedAt       time.Time `json:"created_at"`
}

type store struct {
	root      string
	workspace string
	mu        sync.Mutex
}

type readArgs struct {
	ScopeKind string `json:"scope_kind"`
	ScopeKey  string `json:"scope_key"`
	Limit     int    `json:"limit"`
}

type searchArgs struct {
	Query     string `json:"query"`
	ScopeKind string `json:"scope_kind"`
	ScopeKey  string `json:"scope_key"`
	Limit     int    `json:"limit"`
}

type writeArgs struct {
	ScopeKind       string   `json:"scope_kind"`
	ScopeKey        string   `json:"scope_key"`
	Title           string   `json:"title"`
	Body            string   `json:"body"`
	Summary         string   `json:"summary"`
	Kind            string   `json:"kind"`
	Tags            []string `json:"tags"`
	SourceSpaceID   string   `json:"source_space_id"`
	SourceMessageID string   `json:"source_message_id"`
	CreatedBy       string   `json:"created_by"`
	Confidence      string   `json:"confidence"`
	ExpiresAt       string   `json:"expires_at"`
}

type rememberArgs struct {
	ScopeKind         string   `json:"scope_kind"`
	ScopeKey          string   `json:"scope_key"`
	Title             string   `json:"title"`
	Body              string   `json:"body"`
	Summary           string   `json:"summary"`
	Kind              string   `json:"kind"`
	Tags              []string `json:"tags"`
	SourceSpaceID     string   `json:"source_space_id"`
	SourceMessageID   string   `json:"source_message_id"`
	AuthorizationText string   `json:"authorization_text"`
	Confidence        string   `json:"confidence"`
}

type proposeArgs struct {
	ScopeKind       string   `json:"scope_kind"`
	ScopeKey        string   `json:"scope_key"`
	Title           string   `json:"title"`
	Content         string   `json:"content"`
	Body            string   `json:"body"`
	Kind            string   `json:"kind"`
	Tags            []string `json:"tags"`
	SourceSpaceID   string   `json:"source_space_id"`
	SourceMessageID string   `json:"source_message_id"`
	Reason          string   `json:"reason"`
	Confidence      string   `json:"confidence"`
}

type deleteArgs struct {
	ScopeKind string `json:"scope_kind"`
	ScopeKey  string `json:"scope_key"`
	ID        string `json:"id"`
}

const usageText = "usage: !memory [recent [scope] [limit] | search [scope] <query> | save [scope] <title> :: <body> | proposals | confirm <proposal-id> | reject <proposal-id> | delete [scope] <memory-id>]"
const searchUsageText = "usage: !memory search [scope] <query>"
const saveUsageText = "usage: !memory save [scope] <title> :: <body>"
const confirmUsageText = "usage: !memory confirm <proposal-id>"
const rejectUsageText = "usage: !memory reject <proposal-id>"
const deleteUsageText = "usage: !memory delete [scope] <memory-id>"

func decode[T any](name string, args json.RawMessage, dst *T) error {
	if err := json.Unmarshal(args, dst); err != nil {
		return parseError(name, err)
	}
	return nil
}
