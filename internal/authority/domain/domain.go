package domain

import "errors"

type PrincipalKind string

const (
	PrincipalSystem PrincipalKind = "system"
	PrincipalHuman  PrincipalKind = "human"
	PrincipalAgent  PrincipalKind = "agent"
)

func (kind PrincipalKind) Valid() bool {
	return kind == PrincipalSystem || kind == PrincipalHuman || kind == PrincipalAgent
}

type Principal struct {
	Kind           PrincipalKind
	ID             string
	OrganizationID string
}

func (principal Principal) Valid() bool {
	if principal.OrganizationID == "" {
		return false
	}
	if principal.Kind == PrincipalSystem {
		return principal.ID == ""
	}
	return (principal.Kind == PrincipalHuman || principal.Kind == PrincipalAgent) && principal.ID != ""
}

type ScopeKind string

const (
	ScopeOrganization ScopeKind = "organization"
	ScopeAgent        ScopeKind = "agent"
	ScopeComputer     ScopeKind = "computer"
	ScopeSpace        ScopeKind = "space"
	ScopeWork         ScopeKind = "work"
)

func (kind ScopeKind) Valid() bool {
	switch kind {
	case ScopeOrganization, ScopeAgent, ScopeComputer, ScopeSpace, ScopeWork:
		return true
	default:
		return false
	}
}

type Scope struct {
	Kind ScopeKind
	ID   string
}

func (scope Scope) Valid() bool {
	return scope.Kind.Valid() && scope.ID != ""
}

type Capability string

const (
	CapabilityOrganizationAdmin Capability = "organization.admin"
	CapabilityHumanCreate       Capability = "human.create"
	CapabilityGrantIssue        Capability = "grant.issue"
	CapabilityGrantRevoke       Capability = "grant.revoke"
	CapabilityAuditRead         Capability = "audit.read"
	CapabilityAgentCreate       Capability = "agent.create"
	CapabilityAgentPlace        Capability = "agent.place"
	CapabilitySpaceCreate       Capability = "space.create"
	CapabilitySpaceRead         Capability = "space.read"
	CapabilitySpaceMembers      Capability = "space.members.manage"
	CapabilitySpaceArchive      Capability = "space.archive"
	CapabilityMessageSend       Capability = "message.send"
	CapabilityRunExecute        Capability = "run.execute"
	CapabilityComputerPair      Capability = "computer.pair"
	CapabilityWorkCreate        Capability = "work.create"
	CapabilityWorkRead          Capability = "work.read"
	CapabilityWorkManage        Capability = "work.manage"
	CapabilityWorkApprove       Capability = "work.approve"
)

func (capability Capability) Valid() bool {
	switch capability {
	case CapabilityOrganizationAdmin,
		CapabilityHumanCreate,
		CapabilityGrantIssue,
		CapabilityGrantRevoke,
		CapabilityAuditRead,
		CapabilityAgentCreate,
		CapabilityAgentPlace,
		CapabilitySpaceCreate,
		CapabilitySpaceRead,
		CapabilitySpaceMembers,
		CapabilitySpaceArchive,
		CapabilityMessageSend,
		CapabilityRunExecute,
		CapabilityComputerPair,
		CapabilityWorkCreate,
		CapabilityWorkRead,
		CapabilityWorkManage,
		CapabilityWorkApprove:
		return true
	default:
		return false
	}
}

func (capability Capability) AllowsScope(kind ScopeKind) bool {
	if !capability.Valid() {
		return false
	}
	switch capability {
	case CapabilityWorkCreate:
		return kind == ScopeOrganization
	case CapabilityWorkRead, CapabilityWorkManage, CapabilityWorkApprove:
		return kind == ScopeOrganization || kind == ScopeWork
	default:
		return true
	}
}

var (
	ErrPermissionDenied  = errors.New("permission denied")
	ErrPrincipalNotFound = errors.New("principal not found")
)
