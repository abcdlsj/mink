// Package space defines the multi-participant timeline model that
// replaces the per-source flat session in plugins/desktop. See
// docs/multi-agent-equality.md for the full proposal.
//
// P1 only introduces the data types and an in-memory + file-backed
// store; routing, wake-up, and adapter migrations land in P2-P5.
package space

import "time"

// Kind discriminates the three top-level Space variants users can
// participate in.
type Kind string

const (
	KindChannel    Kind = "channel"
	KindDirectChat Kind = "direct_chat"
	KindAgentDM    Kind = "agent_dm"
)

// Space is the root container of a message timeline. Per the
// proposal, threads are NOT spaces — they live as messages whose
// ParentMessageID points at another message in the same Space.
type Space struct {
	ID           string            `json:"id"`
	Kind         Kind              `json:"kind"`
	Title        string            `json:"title,omitempty"`
	Participants []Participant     `json:"participants"`
	Messages     []Message         `json:"messages"`
	AgentModes   map[string]string `json:"agent_modes,omitempty"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
}
