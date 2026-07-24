package application

import (
	"time"

	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
)

type Action = string

const (
	ActionOrganizationBootstrap Action = "organization.bootstrap"
	ActionHumanCreate           Action = "human.create"
	ActionHumanStatusSet        Action = "human.status.set"
	ActionGrantIssue            Action = "grant.issue"
	ActionGrantRevoke           Action = "grant.revoke"
	ActionAgentCreate           Action = "agent.create"
	ActionAgentProfileUpdate    Action = "agent.profile.update"
	ActionAgentRuntimeConfigure Action = "agent.runtime.configure"
	ActionAgentPlace            Action = "agent.place"
	ActionSpaceCreate           Action = "space.create"
	ActionSpaceMemberAdd        Action = "space.member.add"
	ActionSpaceMemberRemove     Action = "space.member.remove"
	ActionSpaceArchive          Action = "space.archive"
	ActionSpaceUnarchive        Action = "space.unarchive"
	ActionThreadCreate          Action = "thread.create"
	ActionMessageSend           Action = "message.send"
	ActionRunClaim              Action = "run.claim"
	ActionRunRenew              Action = "run.renew"
	ActionRunCancel             Action = "run.cancel"
	ActionRunComplete           Action = "run.complete"
	ActionComputerPairPrepare   Action = "computer.pair.prepare"
	ActionComputerPair          Action = "computer.pair"
	ActionWorkCreate            Action = "work.create"
	ActionWorkAssign            Action = "work.assign"
	ActionWorkTransition        Action = "work.transition"
	ActionWorkApprovalRequest   Action = "work.approval.request"
	ActionWorkApprovalResolve   Action = "work.approval.resolve"
	ActionAuthIdentityBind      Action = "auth.identity.bind"
)

type Event struct {
	Sequence       uint64
	ID             string
	OrganizationID string
	Actor          authoritydomain.Principal
	Action         Action
	TargetKind     string
	TargetID       string
	ContextKind    string
	ContextID      string
	RequestID      string
	Outcome        string
	ReasonCode     string
	OccurredAt     time.Time
}

type AppendCommand struct {
	OrganizationID string
	Actor          authoritydomain.Principal
	Action         Action
	TargetKind     string
	TargetID       string
	ContextKind    string
	ContextID      string
	RequestID      string
	Outcome        string
	ReasonCode     string
	Now            time.Time
}

type ListQuery struct {
	OrganizationID string
	AfterSequence  uint64
	Limit          uint32
}
