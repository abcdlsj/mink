package application

import (
	"time"

	authorityapp "github.com/abcdlsj/sumi/internal/authority/application"
	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
	collaborationapp "github.com/abcdlsj/sumi/internal/collaboration/application"
	executiondomain "github.com/abcdlsj/sumi/internal/execution/domain"
)

const (
	InboxReasonDM             = "dm"
	InboxReasonMention        = "mention"
	InboxReasonThreadFollow   = "thread_follow"
	InboxStateUnread          = "unread"
	InboxStateClaimed         = "claimed"
	InboxStateDone            = "done"
	InboxCompletionSent       = "sent"
	InboxCompletionCancelled  = "cancelled"
	InboxCompletionSilent     = "silent"
	InboxCompletionAccessLost = "access_lost"
	HeldDraftStateHeld        = "held"
	HeldDraftStateSent        = "sent"
	HeldDraftStateCancelled   = "cancelled"
	HeldDraftStateSuperseded  = "superseded"
	HeldDraftStateRetargeted  = "retargeted"
	DraftResolutionRetry      = "retry"
	DraftResolutionCancel     = "cancel"
	DraftResolutionRetarget   = "retarget"
	ResultMessage             = "message"
	ResultHeldDraft           = "held_draft"
)

type InboxItem struct {
	Sequence              uint64
	ID                    string
	Recipient             authoritydomain.Principal
	SpaceID               string
	Target                collaborationapp.MessageTarget
	TriggerMessageID      string
	TriggerTargetSequence uint64
	Reason                string
	State                 string
	ClaimedAt             *time.Time
	DoneAt                *time.Time
	Completion            string
	CreatedAt             time.Time
}

// InboxAuthentication keeps the actor-neutral Inbox contract while retaining
// the runtime proof required to revalidate Agent callers inside the write
// transaction. Human callers carry only their canonical principal.
type InboxAuthentication struct {
	Principal authoritydomain.Principal
	Runtime   authorityapp.RuntimeAuthentication
}

func HumanInboxAuthentication(principal authoritydomain.Principal) InboxAuthentication {
	return InboxAuthentication{Principal: principal}
}

func AgentInboxAuthentication(runtime authorityapp.RuntimeAuthentication) InboxAuthentication {
	return InboxAuthentication{Principal: runtime.Principal, Runtime: runtime}
}

func (authentication InboxAuthentication) Valid() bool {
	switch authentication.Principal.Kind {
	case authoritydomain.PrincipalHuman:
		return authentication.Principal.Valid() && !authentication.Runtime.Valid()
	case authoritydomain.PrincipalAgent:
		return authentication.Runtime.Valid() && authentication.Runtime.Principal == authentication.Principal
	default:
		return false
	}
}

type EligibleInboxTrigger struct {
	Item    InboxItem
	Message collaborationapp.Message
}

type HeldDraft struct {
	Sequence            uint64
	ID                  string
	AgentID             string
	InboxItemID         string
	PredecessorDraftID  string
	SpaceID             string
	Target              collaborationapp.MessageTarget
	BasisTargetSequence uint64
	Body                string
	MentionedPrincipals []authoritydomain.Principal
	HeldReason          string
	State               string
	ResolutionAction    string
	ResultKind          string
	ResultID            string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type InboxNoticeQuery struct {
	Authentication InboxAuthentication
	Now            time.Time
}

type ListInboxItemsQuery struct {
	Authentication InboxAuthentication
	AfterSequence  uint64
	Limit          uint32
	Now            time.Time
}

type ClaimInboxItemCommand struct {
	RequestID      string
	Authentication InboxAuthentication
	InboxItemID    string
	Now            time.Time
}

type ObserveTargetQuery struct {
	Authentication InboxAuthentication
	Target         collaborationapp.MessageTarget
	Limit          uint32
	Now            time.Time
}

type ObserveTargetResult struct {
	Target     collaborationapp.MessageTarget
	Head       uint64
	Messages   []collaborationapp.Message
	ObservedAt time.Time
}

type CompleteInboxItemCommand struct {
	RequestID      string
	Authentication InboxAuthentication
	InboxItemID    string
	Now            time.Time
}

type SetSpaceMuteCommand struct {
	RequestID      string
	Authentication InboxAuthentication
	SpaceID        string
	Muted          bool
	Now            time.Time
}

type SetThreadFollowCommand struct {
	RequestID      string
	Authentication InboxAuthentication
	ThreadID       string
	Followed       bool
	Now            time.Time
}

type InboxPreferenceResult struct {
	Enabled     bool
	CommittedAt time.Time
}

type SendInboxReplyCommand struct {
	RequestID           string
	Authentication      authorityapp.RuntimeAuthentication
	InboxItemID         string
	BasisTargetSequence uint64
	Body                string
	MentionedPrincipals []authoritydomain.Principal
	Now                 time.Time
}

type SendInboxReplyResult struct {
	Kind        string
	Message     *collaborationapp.Message
	HeldDraft   *HeldDraft
	CommittedAt time.Time
}

type ListHeldDraftsQuery struct {
	Authentication authorityapp.RuntimeAuthentication
	AfterSequence  uint64
	Limit          uint32
	Now            time.Time
}

type ListHeldDraftsResult struct {
	Drafts       []HeldDraft
	NextSequence uint64
}

type ResolveHeldDraftCommand struct {
	RequestID           string
	Authentication      authorityapp.RuntimeAuthentication
	HeldDraftID         string
	Action              string
	Target              collaborationapp.MessageTarget
	BasisTargetSequence uint64
	Now                 time.Time
}

type ResolveHeldDraftResult struct {
	Action      string
	Kind        string
	Message     *collaborationapp.Message
	HeldDraft   *HeldDraft
	InboxItem   InboxItem
	CommittedAt time.Time
}

type Run struct {
	Sequence                 uint64
	ID                       string
	AgentID                  string
	InboxItemID              string
	TriggerMessageID         string
	SpaceID                  string
	Target                   collaborationapp.MessageTarget
	TriggerTargetSequence    uint64
	InputBasisTargetSequence uint64
	State                    string
	Attempt                  uint64
	LeaseHolderComputerID    string
	LeaseExpiresAt           *time.Time
	Fence                    uint64
	PlacementDesiredRevision uint64
	ResultKind               string
	ResultID                 string
	Usage                    RunUsage
	ErrorCode                string
	CreatedAt                time.Time
	StartedAt                *time.Time
	CompletedAt              *time.Time
	CancelledAt              *time.Time
}

type RunUsage struct {
	InputUnits  uint64
	OutputUnits uint64
}

type ListRunsQuery struct {
	Authentication authorityapp.RuntimeAuthentication
	AfterSequence  uint64
	Limit          uint32
	Now            time.Time
}

type ListRunsResult struct {
	Runs         []Run
	NextSequence uint64
}

type GetRunQuery struct {
	Authentication authorityapp.RuntimeAuthentication
	RunID          string
	Now            time.Time
}

type ClaimRunCommand struct {
	RequestID      string
	Authentication authorityapp.RuntimeAuthentication
	RunID          string
	Now            time.Time
}

type RenewRunCommand struct {
	RequestID      string
	Authentication authorityapp.RuntimeAuthentication
	RunID          string
	Attempt        uint64
	Fence          uint64
	Now            time.Time
}

type CancelRunCommand struct {
	RequestID      string
	Authentication authorityapp.RuntimeAuthentication
	RunID          string
	Attempt        uint64
	Fence          uint64
	Now            time.Time
}

type CompleteRunCommand struct {
	RequestID           string
	OutboxEventID       string
	Authentication      authorityapp.RuntimeAuthentication
	RunID               string
	Attempt             uint64
	Fence               uint64
	Outcome             executiondomain.Outcome
	ErrorCode           string
	Body                string
	MentionedPrincipals []authoritydomain.Principal
	Usage               RunUsage
	Now                 time.Time
}

type CompleteRunResult struct {
	Run         Run
	Kind        string
	Message     *collaborationapp.Message
	HeldDraft   *HeldDraft
	CommittedAt time.Time
}
