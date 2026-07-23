package store

import (
	"time"

	executionapp "github.com/abcdlsj/sumi/internal/execution/application"
)

const (
	InboxReasonDM           = executionapp.InboxReasonDM
	InboxReasonMention      = executionapp.InboxReasonMention
	InboxReasonThreadFollow = executionapp.InboxReasonThreadFollow

	InboxStateUnread  = executionapp.InboxStateUnread
	InboxStateClaimed = executionapp.InboxStateClaimed
	InboxStateDone    = executionapp.InboxStateDone

	InboxCompletionSent       = executionapp.InboxCompletionSent
	InboxCompletionCancelled  = executionapp.InboxCompletionCancelled
	InboxCompletionSilent     = executionapp.InboxCompletionSilent
	InboxCompletionAccessLost = executionapp.InboxCompletionAccessLost

	HeldDraftStateHeld       = executionapp.HeldDraftStateHeld
	HeldDraftStateSent       = executionapp.HeldDraftStateSent
	HeldDraftStateCancelled  = executionapp.HeldDraftStateCancelled
	HeldDraftStateSuperseded = executionapp.HeldDraftStateSuperseded
	HeldDraftStateRetargeted = executionapp.HeldDraftStateRetargeted

	DraftResolutionRetry    = executionapp.DraftResolutionRetry
	DraftResolutionCancel   = executionapp.DraftResolutionCancel
	DraftResolutionRetarget = executionapp.DraftResolutionRetarget

	InboxResultMessage   = executionapp.ResultMessage
	InboxResultHeldDraft = executionapp.ResultHeldDraft

	operationClaimInboxItem    = "claim"
	operationCompleteInboxItem = "complete"
	operationSetSpaceMute      = "space_mute.set"
	operationSetThreadFollow   = "thread_follow.set"
	operationSendInboxReply    = "reply.send"
	operationResolveHeldDraft  = "draft.resolve"

	maxInboxListLimit = 200
	maxMentionCount   = 64
)

type InboxItem = executionapp.InboxItem

type EligibleInboxTrigger = executionapp.EligibleInboxTrigger

type HeldDraft = executionapp.HeldDraft

type InboxNoticeParams = executionapp.InboxNoticeQuery

type InboxAuthentication = executionapp.InboxAuthentication

type ListInboxItemsParams = executionapp.ListInboxItemsQuery

type ClaimInboxItemParams = executionapp.ClaimInboxItemCommand

type ObserveTargetParams = executionapp.ObserveTargetQuery

type ObserveTargetResult = executionapp.ObserveTargetResult

type CompleteInboxItemParams = executionapp.CompleteInboxItemCommand

type SetSpaceMuteParams = executionapp.SetSpaceMuteCommand

type SetThreadFollowParams = executionapp.SetThreadFollowCommand

type InboxPreferenceResult = executionapp.InboxPreferenceResult

type SendInboxReplyParams = executionapp.SendInboxReplyCommand

type SendInboxReplyResult = executionapp.SendInboxReplyResult

type ListHeldDraftsParams = executionapp.ListHeldDraftsQuery

type ListHeldDraftsResult = executionapp.ListHeldDraftsResult

type ResolveHeldDraftParams = executionapp.ResolveHeldDraftCommand

type ResolveHeldDraftResult = executionapp.ResolveHeldDraftResult

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
