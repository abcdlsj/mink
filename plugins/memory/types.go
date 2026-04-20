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
	ID        string
	ScopeKind string
	ScopeKey  string
	Title     string
	Body      string
	Summary   string
	Kind      string
	Tags      []string
	Source    string
	Path      string
	CreatedAt time.Time
	UpdatedAt time.Time
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
	ScopeKind string   `json:"scope_kind"`
	ScopeKey  string   `json:"scope_key"`
	Title     string   `json:"title"`
	Body      string   `json:"body"`
	Summary   string   `json:"summary"`
	Kind      string   `json:"kind"`
	Tags      []string `json:"tags"`
}

const usageText = "usage: !memory [recent [scope] [limit] | search [scope] <query> | save [scope] <title> :: <body>]"
const searchUsageText = "usage: !memory search [scope] <query>"
const saveUsageText = "usage: !memory save [scope] <title> :: <body>"

func decode[T any](name string, args json.RawMessage, dst *T) error {
	if err := json.Unmarshal(args, dst); err != nil {
		return parseError(name, err)
	}
	return nil
}
