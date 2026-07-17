package space

import (
	"time"

	"github.com/abcdlsj/sumi/msg"
)

// RoutingIntent is the immutable record of who a message should wake and why,
// persisted in the SAME Space file commit as the message that produced it. It
// is the durable authority for recovery: startup reconciliation reads persisted
// intents to rebuild deliveries rather than re-deriving routing from the current
// (possibly changed) config or in-memory chain tracker.
//
// Only immutable facts live here. Mutable execution state (lease, attempt,
// error) belongs to a Delivery, never to a Message. ChainRoot is always set —
// including the root user message's own first-round mention/listen intents,
// whose ChainRoot is that root message's ID — so routing-budget accounting
// (DefaultBudget - count(chainRoot)) never undercounts the first round.
type RoutingIntent struct {
	AgentID         string `json:"agent_id"`
	Kind            string `json:"kind"` // mention / listen / chain
	Reason          string `json:"reason,omitempty"`
	ParentMessageID string `json:"parent_message_id,omitempty"`
	ChainRoot       string `json:"chain_root"`
}

// Message is the product-visible chat fact. Desktop history, channel/thread
// views, DMs, attachments, tasks, and transcript quotes must be derived from
// Space messages, not from runtime sessions or runlogs.
type Message struct {
	ID              string            `json:"id"`
	SpaceID         string            `json:"space_id"`
	ParentMessageID string            `json:"parent_message_id,omitempty"`
	AuthorID        string            `json:"author_id"`
	AuthorKind      ParticipantKind   `json:"author_kind"`
	Content         string            `json:"content,omitempty"`
	Attachments     []msg.Attachment  `json:"attachments,omitempty"`
	Reasoning       string            `json:"reasoning,omitempty"`
	Status          string            `json:"status,omitempty"`
	Error           string            `json:"error,omitempty"`
	Mentions        []string          `json:"mentions,omitempty"`
	AutoReplyReason string            `json:"auto_reply_reason,omitempty"`
	Usage           *msg.TokenUsage   `json:"usage,omitempty"`
	RuntimeMeta     map[string]string `json:"runtime_meta,omitempty"`
	// RoutingIntents are the immutable wake intents persisted with this message.
	// Empty means no routed collaboration (direct path or plain chat).
	RoutingIntents []RoutingIntent `json:"routing_intents,omitempty"`
	// DeliveryID is the stable durable-delivery this assistant message projects.
	// Empty for user messages, direct-path replies, or pre-Phase-2 messages. It
	// is the append-once key that keeps one visible message across
	// pending/chunk/fail/retry/success.
	DeliveryID string `json:"delivery_id,omitempty"`
	// The write-side fence for routed worker finalizes is NOT stored on the
	// message. Authority is "who owns the live Delivery lease now", re-read from
	// the Delivery record inside the store's save critical section
	// (Store.SaveSpaceUnderDeliveryFence) — a message-level version could only
	// prove "highest WRITTEN version", not "highest CLAIMED fence", and would
	// reopen the crash window where a superseded worker writes before the newer
	// owner does.
	CreatedAt time.Time `json:"created_at"`
}
