package space

import (
	"time"

	"github.com/abcdlsj/sumi/msg"
)

type Message struct {
	ID              string            `json:"id"`
	SpaceID         string            `json:"space_id"`
	ParentMessageID string            `json:"parent_message_id,omitempty"`
	AuthorID        string            `json:"author_id"`
	AuthorKind      ParticipantKind   `json:"author_kind"`
	Content         string            `json:"content,omitempty"`
	Reasoning       string            `json:"reasoning,omitempty"`
	Status          string            `json:"status,omitempty"`
	Error           string            `json:"error,omitempty"`
	Mentions        []string          `json:"mentions,omitempty"`
	AutoReplyReason string            `json:"auto_reply_reason,omitempty"`
	Usage           *msg.TokenUsage   `json:"usage,omitempty"`
	RuntimeMeta     map[string]string `json:"runtime_meta,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
}
