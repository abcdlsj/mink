package domain

import "errors"

type PrincipalKind string

const (
	PrincipalSystem PrincipalKind = "system"
	PrincipalHuman  PrincipalKind = "human"
	PrincipalAgent  PrincipalKind = "agent"
)

var validKinds = map[PrincipalKind]bool{
	PrincipalSystem: true,
	PrincipalHuman:  true,
	PrincipalAgent:  true,
}

func (k PrincipalKind) Valid() bool { return validKinds[k] }

type Principal struct {
	Kind           PrincipalKind
	ID             string
	OrganizationID string
}

func (p Principal) Valid() bool {
	if p.OrganizationID == "" {
		return false
	}
	switch p.Kind {
	case PrincipalSystem:
		return p.ID == ""
	case PrincipalHuman, PrincipalAgent:
		return p.ID != ""
	}
	return false
}

type ScopeKind string

const (
	ScopeOrganization ScopeKind = "organization"
	ScopeAgent        ScopeKind = "agent"
	ScopeComputer     ScopeKind = "computer"
	ScopeSpace        ScopeKind = "space"
	ScopeWork         ScopeKind = "work"
)

var validScopeKinds = map[ScopeKind]bool{
	ScopeOrganization: true,
	ScopeAgent:        true,
	ScopeComputer:     true,
	ScopeSpace:        true,
	ScopeWork:         true,
}

func (k ScopeKind) Valid() bool { return validScopeKinds[k] }

type Scope struct {
	Kind ScopeKind
	ID   string
}

func (s Scope) Valid() bool { return s.Kind.Valid() && s.ID != "" }

type Capability string

const (
	CapabilityOrganizationAdmin     Capability = "organization.admin"
	CapabilityHumanCreate           Capability = "human.create"
	CapabilityGrantIssue            Capability = "grant.issue"
	CapabilityGrantRevoke           Capability = "grant.revoke"
	CapabilityAuditRead             Capability = "audit.read"
	CapabilityAgentCreate           Capability = "agent.create"
	CapabilityAgentProfileUpdate    Capability = "agent.profile.update"
	CapabilityAgentRuntimeConfigure Capability = "agent.runtime.configure"
	CapabilityAgentPlace            Capability = "agent.place"
	CapabilitySpaceCreate           Capability = "space.create"
	CapabilitySpaceRead             Capability = "space.read"
	CapabilitySpaceMembers          Capability = "space.members.manage"
	CapabilitySpaceArchive          Capability = "space.archive"
	CapabilityMessageSend           Capability = "message.send"
	CapabilityRunExecute            Capability = "run.execute"
	CapabilityComputerPair          Capability = "computer.pair"
	CapabilityWorkCreate            Capability = "work.create"
	CapabilityWorkRead              Capability = "work.read"
	CapabilityWorkManage            Capability = "work.manage"
	CapabilityWorkApprove           Capability = "work.approve"
)

var validCap = func() map[Capability]bool {
	m := make(map[Capability]bool, 20)
	for _, c := range []Capability{
		CapabilityOrganizationAdmin, CapabilityHumanCreate,
		CapabilityGrantIssue, CapabilityGrantRevoke, CapabilityAuditRead,
		CapabilityAgentCreate, CapabilityAgentProfileUpdate, CapabilityAgentRuntimeConfigure,
		CapabilityAgentPlace, CapabilitySpaceCreate, CapabilitySpaceRead, CapabilitySpaceMembers,
		CapabilitySpaceArchive, CapabilityMessageSend, CapabilityRunExecute, CapabilityComputerPair,
		CapabilityWorkCreate, CapabilityWorkRead, CapabilityWorkManage, CapabilityWorkApprove,
	} {
		m[c] = true
	}
	return m
}()

func (c Capability) Valid() bool { return validCap[c] }

func (c Capability) AllowsScope(kind ScopeKind) bool {
	if !c.Valid() {
		return false
	}
	switch c {
	case CapabilityWorkCreate:
		return kind == ScopeOrganization
	case CapabilityWorkRead, CapabilityWorkManage, CapabilityWorkApprove:
		return kind == ScopeOrganization || kind == ScopeWork
	}
	return true
}

var (
	ErrPermissionDenied  = errors.New("permission denied")
	ErrPrincipalNotFound = errors.New("principal not found")
)
