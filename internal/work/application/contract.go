package application

import (
	"errors"
	"time"

	authorityapp "github.com/abcdlsj/sumi/internal/authority/application"
	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
	collaborationapp "github.com/abcdlsj/sumi/internal/collaboration/application"
)

const (
	StateOpen            = "open"
	StateBlocked         = "blocked"
	StateWaitingApproval = "waiting_approval"
	StateCompleted       = "completed"
	StateFailed          = "failed"
	StateCancelled       = "cancelled"

	AssignmentCoordinator = "coordinator"
	AssignmentContributor = "contributor"
)

var (
	ErrNotFound             = errors.New("work not found")
	ErrRequestConflict      = errors.New("work request conflict")
	ErrInvalid              = errors.New("work invalid")
	ErrTransitionInvalid    = errors.New("invalid work transition")
	ErrTerminal             = errors.New("work is terminal")
	ErrAcceptanceIncomplete = errors.New("work acceptance criteria incomplete")
	ErrApprovalNotFound     = errors.New("work approval not found")
	ErrApprovalConflict     = errors.New("work approval conflict")
	ErrAssignmentConflict   = errors.New("work assignment conflict")
	ErrPlacementInvalid     = errors.New("work assignment placement invalid")
	ErrCursorUnavailable    = errors.New("work cursor unavailable")
)

type Work struct {
	ID                   string
	OrganizationID       string
	RootWorkID           string
	ParentWorkID         string
	SourceMessageID      string
	SourceSpaceID        string
	SourceTarget         collaborationapp.MessageTarget
	SourceTargetSequence uint64
	TeamSpaceID          string
	Goal                 string
	State                string
	BlockingReason       string
	Result               string
	Creator              authoritydomain.Principal
	CreatedAt            time.Time
	UpdatedAt            time.Time
	StateChangedAt       time.Time
	CompletedAt          *time.Time
	FailedAt             *time.Time
	CancelledAt          *time.Time
	Constraints          []Text
	AcceptanceCriteria   []Criterion
}

type Text struct {
	ID        string
	Ordinal   uint32
	Body      string
	CreatedAt time.Time
}

type Criterion struct {
	ID        string
	Ordinal   uint32
	Body      string
	CreatedAt time.Time
}

type CreateCommand struct {
	RequestID            string
	Actor                authoritydomain.Principal
	Agent                authorityapp.RuntimeAuthentication
	Run                  *authorityapp.RunProof
	ParentWorkID         string
	SourceMessageID      string
	SourceSpaceID        string
	SourceTarget         collaborationapp.MessageTarget
	SourceTargetSequence uint64
	Goal                 string
	Constraints          []string
	AcceptanceCriteria   []string
	Now                  time.Time
}

type ReadQuery struct {
	Actor  authoritydomain.Principal
	Agent  authorityapp.RuntimeAuthentication
	WorkID string
	Now    time.Time
}

type ListQuery struct {
	Actor  authoritydomain.Principal
	Agent  authorityapp.RuntimeAuthentication
	Cursor string
	Limit  uint32
	Now    time.Time
}

type Page struct {
	Works      []Work
	NextCursor string
}

type Assignment struct {
	ID                             string
	WorkID                         string
	OrganizationID                 string
	Role                           string
	AgentID                        string
	HolderComputerID               string
	HolderPlacementDesiredRevision uint64
	AssignedBy                     authoritydomain.Principal
	AssignedAt                     time.Time
	EndedAt                        *time.Time
	EndReason                      string
}

type AssignCommand struct {
	RequestID string
	Actor     authoritydomain.Principal
	Agent     authorityapp.RuntimeAuthentication
	Run       *authorityapp.RunProof
	WorkID    string
	Role      string
	AgentID   string
	Now       time.Time
}

type CriterionResultInput struct {
	CriterionID string
	Verdict     string
	Evidence    string
}

type TransitionCommand struct {
	RequestID        string
	Actor            authoritydomain.Principal
	Agent            authorityapp.RuntimeAuthentication
	Run              *authorityapp.RunProof
	WorkID           string
	ToState          string
	Reason           string
	Result           string
	CriterionResults []CriterionResultInput
	Now              time.Time
}

type RequestApprovalCommand struct {
	RequestID string
	Actor     authoritydomain.Principal
	Agent     authorityapp.RuntimeAuthentication
	Run       *authorityapp.RunProof
	WorkID    string
	Question  string
	Now       time.Time
}

type ResolveApprovalCommand struct {
	RequestID  string
	Actor      authoritydomain.Principal
	ApprovalID string
	Decision   string
	Note       string
	Now        time.Time
}

type Approval struct {
	ID               string
	WorkID           string
	OrganizationID   string
	Status           string
	Question         string
	RequestedBy      authoritydomain.Principal
	RequestedAt      time.Time
	DecidedByHumanID string
	DecisionNote     string
	DecidedAt        *time.Time
}

type CriterionResult struct {
	Sequence       uint64
	ID             string
	WorkID         string
	OrganizationID string
	CriterionID    string
	Verdict        string
	Evidence       string
	Actor          authoritydomain.Principal
	OccurredAt     time.Time
}

type Event struct {
	Sequence       uint64
	ID             string
	WorkID         string
	OrganizationID string
	Kind           string
	Actor          authoritydomain.Principal
	FromState      string
	ToState        string
	ReferenceKind  string
	ReferenceID    string
	Reason         string
	OccurredAt     time.Time
}

type Detail struct {
	Work
	Assignments      []Assignment
	Approvals        []Approval
	CriterionResults []CriterionResult
	Events           []Event
}

type AttentionItem struct {
	WorkID     string
	SpaceID    string
	AgentID    string
	Kind       string
	Status     string
	ReasonCode string
	UpdatedAt  time.Time
}

type AttentionQuery struct {
	Human authoritydomain.Principal
	Limit uint32
	Now   time.Time
}
