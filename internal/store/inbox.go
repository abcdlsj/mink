package store

import "time"

const (
	InboxReasonDM           = "dm"
	InboxReasonMention      = "mention"
	InboxReasonThreadFollow = "thread_follow"

	InboxStateUnread  = "unread"
	InboxStateClaimed = "claimed"
	InboxStateDone    = "done"

	InboxCompletionSent       = "sent"
	InboxCompletionCancelled  = "cancelled"
	InboxCompletionSilent     = "silent"
	InboxCompletionAccessLost = "access_lost"

	HeldDraftStateHeld       = "held"
	HeldDraftStateSent       = "sent"
	HeldDraftStateCancelled  = "cancelled"
	HeldDraftStateSuperseded = "superseded"
	HeldDraftStateRetargeted = "retargeted"

	DraftResolutionRetry    = "retry"
	DraftResolutionCancel   = "cancel"
	DraftResolutionRetarget = "retarget"

	InboxResultMessage   = "message"
	InboxResultHeldDraft = "held_draft"

	operationClaimInboxItem    = "claim"
	operationCompleteInboxItem = "complete"
	operationSetSpaceMute      = "space_mute.set"
	operationSetThreadFollow   = "thread_follow.set"
	operationSendInboxReply    = "reply.send"
	operationResolveHeldDraft  = "draft.resolve"

	maxInboxListLimit = 200
	maxMentionCount   = 64
)

type InboxItem struct {
	Sequence              uint64
	ID                    string
	AgentID               string
	SpaceID               string
	Target                MessageTarget
	TriggerMessageID      string
	TriggerTargetSequence uint64
	Reason                string
	State                 string
	ClaimedAt             *time.Time
	DoneAt                *time.Time
	Completion            string
	CreatedAt             time.Time
}

type EligibleInboxTrigger struct {
	Item    InboxItem
	Message Message
}

type HeldDraft struct {
	Sequence            uint64
	ID                  string
	AgentID             string
	InboxItemID         string
	PredecessorDraftID  string
	SpaceID             string
	Target              MessageTarget
	BasisTargetSequence uint64
	Body                string
	MentionedAgentIDs   []string
	HeldReason          string
	State               string
	ResolutionAction    string
	ResultKind          string
	ResultID            string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type InboxNoticeParams struct {
	Authentication AgentRuntimeAuthentication
	Now            time.Time
}

type ListInboxItemsParams struct {
	Authentication AgentRuntimeAuthentication
	AfterSequence  uint64
	Limit          uint32
	Now            time.Time
}

type ClaimInboxItemParams struct {
	RequestID      string
	Authentication AgentRuntimeAuthentication
	InboxItemID    string
	Now            time.Time
}

type ObserveTargetParams struct {
	Authentication AgentRuntimeAuthentication
	Target         MessageTarget
	Limit          uint32
	Now            time.Time
}

type ObserveTargetResult struct {
	Target     MessageTarget
	Head       uint64
	Messages   []Message
	ObservedAt time.Time
}

type CompleteInboxItemParams struct {
	RequestID      string
	Authentication AgentRuntimeAuthentication
	InboxItemID    string
	Now            time.Time
}

type SetSpaceMuteParams struct {
	RequestID      string
	Authentication AgentRuntimeAuthentication
	SpaceID        string
	Muted          bool
	Now            time.Time
}

type SetThreadFollowParams struct {
	RequestID      string
	Authentication AgentRuntimeAuthentication
	ThreadID       string
	Followed       bool
	Now            time.Time
}

type InboxPreferenceResult struct {
	Enabled     bool
	CommittedAt time.Time
}

type SendInboxReplyParams struct {
	RequestID           string
	Authentication      AgentRuntimeAuthentication
	InboxItemID         string
	BasisTargetSequence uint64
	Body                string
	MentionedAgentIDs   []string
	Now                 time.Time
}

type SendInboxReplyResult struct {
	Kind        string
	Message     *Message
	HeldDraft   *HeldDraft
	CommittedAt time.Time
}

type ListHeldDraftsParams struct {
	Authentication AgentRuntimeAuthentication
	AfterSequence  uint64
	Limit          uint32
	Now            time.Time
}

type ListHeldDraftsResult struct {
	Drafts       []HeldDraft
	NextSequence uint64
}

type ResolveHeldDraftParams struct {
	RequestID           string
	Authentication      AgentRuntimeAuthentication
	HeldDraftID         string
	Action              string
	Target              MessageTarget
	BasisTargetSequence uint64
	Now                 time.Time
}

type ResolveHeldDraftResult struct {
	Action      string
	Kind        string
	Message     *Message
	HeldDraft   *HeldDraft
	InboxItem   InboxItem
	CommittedAt time.Time
}

type inboxItemRequestReceipt struct {
	Item InboxItem `json:"item"`
}

type inboxPreferenceRequestReceipt struct {
	Enabled     bool      `json:"enabled"`
	CommittedAt time.Time `json:"committed_at"`
}

type inboxMessageReference struct {
	ID             string        `json:"id"`
	AgentID        string        `json:"agent_id"`
	SpaceID        string        `json:"space_id"`
	Target         MessageTarget `json:"target"`
	TargetSequence uint64        `json:"target_sequence"`
	MentionCount   int           `json:"mention_count"`
	CreatedAt      time.Time     `json:"created_at"`
}

type inboxHeldDraftReceipt struct {
	Sequence            uint64        `json:"sequence"`
	ID                  string        `json:"id"`
	AgentID             string        `json:"agent_id"`
	InboxItemID         string        `json:"inbox_item_id"`
	PredecessorDraftID  string        `json:"predecessor_draft_id,omitempty"`
	SpaceID             string        `json:"space_id"`
	Target              MessageTarget `json:"target"`
	BasisTargetSequence uint64        `json:"basis_target_sequence"`
	MentionCount        int           `json:"mention_count"`
	HeldReason          string        `json:"held_reason"`
	State               string        `json:"state"`
	ResolutionAction    string        `json:"resolution_action,omitempty"`
	ResultKind          string        `json:"result_kind,omitempty"`
	ResultID            string        `json:"result_id,omitempty"`
	CreatedAt           time.Time     `json:"created_at"`
	UpdatedAt           time.Time     `json:"updated_at"`
}

type inboxSendRequestReceipt struct {
	InboxItemID string                 `json:"inbox_item_id"`
	Kind        string                 `json:"kind"`
	Message     *inboxMessageReference `json:"message,omitempty"`
	HeldDraft   *inboxHeldDraftReceipt `json:"held_draft,omitempty"`
	CommittedAt time.Time              `json:"committed_at"`
}

type inboxResolveRequestReceipt struct {
	SourceDraftID string                 `json:"source_draft_id"`
	Action        string                 `json:"action"`
	Kind          string                 `json:"kind,omitempty"`
	Message       *inboxMessageReference `json:"message,omitempty"`
	HeldDraft     *inboxHeldDraftReceipt `json:"held_draft,omitempty"`
	InboxItem     InboxItem              `json:"inbox_item"`
	CommittedAt   time.Time              `json:"committed_at"`
}
