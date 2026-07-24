package grant

import (
	"errors"

	"connectrpc.com/connect"
	grantv1 "github.com/abcdlsj/sumi/gen/go/sumi/grant/v1"
	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
	"github.com/abcdlsj/sumi/internal/transport/connectid"
)

func principalParams(principal *grantv1.Principal, organizationID string, allowSystem bool) (authoritydomain.Principal, error) {
	if principal == nil {
		return authoritydomain.Principal{}, connect.NewError(connect.CodeInvalidArgument, errors.New("principal is required"))
	}
	var kind authoritydomain.PrincipalKind
	if principal.GetKind() == grantv1.PrincipalKind_PRINCIPAL_KIND_HUMAN {
		kind = authoritydomain.PrincipalHuman
	} else if principal.GetKind() == grantv1.PrincipalKind_PRINCIPAL_KIND_AGENT {
		kind = authoritydomain.PrincipalAgent
	} else if allowSystem && principal.GetKind() == grantv1.PrincipalKind_PRINCIPAL_KIND_SYSTEM {
		kind = authoritydomain.PrincipalSystem
	}
	if kind == "" {
		return authoritydomain.Principal{}, connect.NewError(connect.CodeInvalidArgument, errors.New("principal kind is invalid"))
	}
	id, err := connectid.CanonicalID(principal.GetId(), "principal id")
	if err != nil {
		return authoritydomain.Principal{}, err
	}
	return authoritydomain.Principal{Kind: kind, ID: id, OrganizationID: organizationID}, nil
}

func scopeParams(scope *grantv1.Scope, organizationID string) (authoritydomain.Scope, error) {
	if scope == nil {
		return authoritydomain.Scope{}, connect.NewError(connect.CodeInvalidArgument, errors.New("scope is required"))
	}
	var kind authoritydomain.ScopeKind
	switch scope.GetKind() {
	case grantv1.ScopeKind_SCOPE_KIND_ORGANIZATION:
		kind = authoritydomain.ScopeOrganization
	case grantv1.ScopeKind_SCOPE_KIND_AGENT:
		kind = authoritydomain.ScopeAgent
	case grantv1.ScopeKind_SCOPE_KIND_COMPUTER:
		kind = authoritydomain.ScopeComputer
	case grantv1.ScopeKind_SCOPE_KIND_SPACE:
		kind = authoritydomain.ScopeSpace
	case grantv1.ScopeKind_SCOPE_KIND_WORK:
		kind = authoritydomain.ScopeWork
	}
	if kind == "" {
		return authoritydomain.Scope{}, connect.NewError(connect.CodeInvalidArgument, errors.New("scope kind is invalid"))
	}
	id, err := connectid.CanonicalID(scope.GetId(), "scope id")
	if err != nil {
		return authoritydomain.Scope{}, err
	}
	if kind == authoritydomain.ScopeOrganization && id != organizationID {
		return authoritydomain.Scope{}, connect.NewError(connect.CodePermissionDenied, errors.New("cross-organization scope denied"))
	}
	return authoritydomain.Scope{Kind: kind, ID: id}, nil
}

type capabilityMapping struct {
	proto  grantv1.Capability
	domain authoritydomain.Capability
}

var capabilityMappings = [...]capabilityMapping{
	{grantv1.Capability_CAPABILITY_ORGANIZATION_ADMIN, authoritydomain.CapabilityOrganizationAdmin},
	{grantv1.Capability_CAPABILITY_HUMAN_CREATE, authoritydomain.CapabilityHumanCreate},
	{grantv1.Capability_CAPABILITY_GRANT_ISSUE, authoritydomain.CapabilityGrantIssue},
	{grantv1.Capability_CAPABILITY_GRANT_REVOKE, authoritydomain.CapabilityGrantRevoke},
	{grantv1.Capability_CAPABILITY_AUDIT_READ, authoritydomain.CapabilityAuditRead},
	{grantv1.Capability_CAPABILITY_AGENT_CREATE, authoritydomain.CapabilityAgentCreate},
	{grantv1.Capability_CAPABILITY_AGENT_PROFILE_UPDATE, authoritydomain.CapabilityAgentProfileUpdate},
	{grantv1.Capability_CAPABILITY_AGENT_RUNTIME_CONFIGURE, authoritydomain.CapabilityAgentRuntimeConfigure},
	{grantv1.Capability_CAPABILITY_AGENT_PLACE, authoritydomain.CapabilityAgentPlace},
	{grantv1.Capability_CAPABILITY_SPACE_CREATE, authoritydomain.CapabilitySpaceCreate},
	{grantv1.Capability_CAPABILITY_SPACE_READ, authoritydomain.CapabilitySpaceRead},
	{grantv1.Capability_CAPABILITY_SPACE_MEMBERS_MANAGE, authoritydomain.CapabilitySpaceMembers},
	{grantv1.Capability_CAPABILITY_SPACE_ARCHIVE, authoritydomain.CapabilitySpaceArchive},
	{grantv1.Capability_CAPABILITY_MESSAGE_SEND, authoritydomain.CapabilityMessageSend},
	{grantv1.Capability_CAPABILITY_RUN_EXECUTE, authoritydomain.CapabilityRunExecute},
	{grantv1.Capability_CAPABILITY_COMPUTER_PAIR, authoritydomain.CapabilityComputerPair},
	{grantv1.Capability_CAPABILITY_WORK_CREATE, authoritydomain.CapabilityWorkCreate},
	{grantv1.Capability_CAPABILITY_WORK_READ, authoritydomain.CapabilityWorkRead},
	{grantv1.Capability_CAPABILITY_WORK_MANAGE, authoritydomain.CapabilityWorkManage},
	{grantv1.Capability_CAPABILITY_WORK_APPROVE, authoritydomain.CapabilityWorkApprove},
}

func capabilityName(value grantv1.Capability) (authoritydomain.Capability, bool) {
	for _, mapping := range capabilityMappings {
		if mapping.proto == value {
			return mapping.domain, true
		}
	}
	return "", false
}

func capabilityValue(value authoritydomain.Capability) grantv1.Capability {
	for _, mapping := range capabilityMappings {
		if mapping.domain == value {
			return mapping.proto
		}
	}
	return grantv1.Capability_CAPABILITY_UNSPECIFIED
}
