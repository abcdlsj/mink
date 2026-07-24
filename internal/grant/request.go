package grant

import (
	"errors"

	"connectrpc.com/connect"
	grantv1 "github.com/abcdlsj/sumi/gen/go/sumi/grant/v1"
	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
	"github.com/abcdlsj/sumi/internal/servicesvc"
	"github.com/abcdlsj/sumi/internal/transport/id"
)

var (
	capFromProto = map[grantv1.Capability]authoritydomain.Capability{
		grantv1.Capability_CAPABILITY_ORGANIZATION_ADMIN:    authoritydomain.CapabilityOrganizationAdmin,
		grantv1.Capability_CAPABILITY_HUMAN_CREATE:          authoritydomain.CapabilityHumanCreate,
		grantv1.Capability_CAPABILITY_GRANT_ISSUE:           authoritydomain.CapabilityGrantIssue,
		grantv1.Capability_CAPABILITY_GRANT_REVOKE:          authoritydomain.CapabilityGrantRevoke,
		grantv1.Capability_CAPABILITY_AUDIT_READ:            authoritydomain.CapabilityAuditRead,
		grantv1.Capability_CAPABILITY_AGENT_CREATE:          authoritydomain.CapabilityAgentCreate,
		grantv1.Capability_CAPABILITY_AGENT_PROFILE_UPDATE:  authoritydomain.CapabilityAgentProfileUpdate,
		grantv1.Capability_CAPABILITY_AGENT_RUNTIME_CONFIGURE: authoritydomain.CapabilityAgentRuntimeConfigure,
		grantv1.Capability_CAPABILITY_AGENT_PLACE:           authoritydomain.CapabilityAgentPlace,
		grantv1.Capability_CAPABILITY_SPACE_CREATE:          authoritydomain.CapabilitySpaceCreate,
		grantv1.Capability_CAPABILITY_SPACE_READ:            authoritydomain.CapabilitySpaceRead,
		grantv1.Capability_CAPABILITY_SPACE_MEMBERS_MANAGE:  authoritydomain.CapabilitySpaceMembers,
		grantv1.Capability_CAPABILITY_SPACE_ARCHIVE:         authoritydomain.CapabilitySpaceArchive,
		grantv1.Capability_CAPABILITY_MESSAGE_SEND:          authoritydomain.CapabilityMessageSend,
		grantv1.Capability_CAPABILITY_RUN_EXECUTE:           authoritydomain.CapabilityRunExecute,
		grantv1.Capability_CAPABILITY_COMPUTER_PAIR:         authoritydomain.CapabilityComputerPair,
		grantv1.Capability_CAPABILITY_WORK_CREATE:           authoritydomain.CapabilityWorkCreate,
		grantv1.Capability_CAPABILITY_WORK_READ:             authoritydomain.CapabilityWorkRead,
		grantv1.Capability_CAPABILITY_WORK_MANAGE:           authoritydomain.CapabilityWorkManage,
		grantv1.Capability_CAPABILITY_WORK_APPROVE:          authoritydomain.CapabilityWorkApprove,
	}
	capToProto = map[authoritydomain.Capability]grantv1.Capability{
		authoritydomain.CapabilityOrganizationAdmin:    grantv1.Capability_CAPABILITY_ORGANIZATION_ADMIN,
		authoritydomain.CapabilityHumanCreate:          grantv1.Capability_CAPABILITY_HUMAN_CREATE,
		authoritydomain.CapabilityGrantIssue:           grantv1.Capability_CAPABILITY_GRANT_ISSUE,
		authoritydomain.CapabilityGrantRevoke:          grantv1.Capability_CAPABILITY_GRANT_REVOKE,
		authoritydomain.CapabilityAuditRead:            grantv1.Capability_CAPABILITY_AUDIT_READ,
		authoritydomain.CapabilityAgentCreate:          grantv1.Capability_CAPABILITY_AGENT_CREATE,
		authoritydomain.CapabilityAgentProfileUpdate:   grantv1.Capability_CAPABILITY_AGENT_PROFILE_UPDATE,
		authoritydomain.CapabilityAgentRuntimeConfigure: grantv1.Capability_CAPABILITY_AGENT_RUNTIME_CONFIGURE,
		authoritydomain.CapabilityAgentPlace:           grantv1.Capability_CAPABILITY_AGENT_PLACE,
		authoritydomain.CapabilitySpaceCreate:          grantv1.Capability_CAPABILITY_SPACE_CREATE,
		authoritydomain.CapabilitySpaceRead:            grantv1.Capability_CAPABILITY_SPACE_READ,
		authoritydomain.CapabilitySpaceMembers:         grantv1.Capability_CAPABILITY_SPACE_MEMBERS_MANAGE,
		authoritydomain.CapabilitySpaceArchive:         grantv1.Capability_CAPABILITY_SPACE_ARCHIVE,
		authoritydomain.CapabilityMessageSend:          grantv1.Capability_CAPABILITY_MESSAGE_SEND,
		authoritydomain.CapabilityRunExecute:           grantv1.Capability_CAPABILITY_RUN_EXECUTE,
		authoritydomain.CapabilityComputerPair:         grantv1.Capability_CAPABILITY_COMPUTER_PAIR,
		authoritydomain.CapabilityWorkCreate:           grantv1.Capability_CAPABILITY_WORK_CREATE,
		authoritydomain.CapabilityWorkRead:             grantv1.Capability_CAPABILITY_WORK_READ,
		authoritydomain.CapabilityWorkManage:           grantv1.Capability_CAPABILITY_WORK_MANAGE,
		authoritydomain.CapabilityWorkApprove:          grantv1.Capability_CAPABILITY_WORK_APPROVE,
	}
	scopeFromProto = map[grantv1.ScopeKind]authoritydomain.ScopeKind{
		grantv1.ScopeKind_SCOPE_KIND_ORGANIZATION: authoritydomain.ScopeOrganization,
		grantv1.ScopeKind_SCOPE_KIND_AGENT:        authoritydomain.ScopeAgent,
		grantv1.ScopeKind_SCOPE_KIND_COMPUTER:     authoritydomain.ScopeComputer,
		grantv1.ScopeKind_SCOPE_KIND_SPACE:        authoritydomain.ScopeSpace,
		grantv1.ScopeKind_SCOPE_KIND_WORK:         authoritydomain.ScopeWork,
	}
	scopeToProto = map[authoritydomain.ScopeKind]grantv1.ScopeKind{
		authoritydomain.ScopeOrganization: grantv1.ScopeKind_SCOPE_KIND_ORGANIZATION,
		authoritydomain.ScopeAgent:        grantv1.ScopeKind_SCOPE_KIND_AGENT,
		authoritydomain.ScopeComputer:     grantv1.ScopeKind_SCOPE_KIND_COMPUTER,
		authoritydomain.ScopeSpace:        grantv1.ScopeKind_SCOPE_KIND_SPACE,
		authoritydomain.ScopeWork:         grantv1.ScopeKind_SCOPE_KIND_WORK,
	}
)

func capName(v grantv1.Capability) (authoritydomain.Capability, bool) {
	c, ok := capFromProto[v]
	return c, ok
}

func capValue(v authoritydomain.Capability) grantv1.Capability {
	c, ok := capToProto[v]
	if !ok {
		return grantv1.Capability_CAPABILITY_UNSPECIFIED
	}
	return c
}

func parsePrincipal(principal *grantv1.Principal, orgID string, allowSystem bool) (authoritydomain.Principal, error) {
	if principal == nil {
		return authoritydomain.Principal{}, servicesvc.InvalArg("principal is required")
	}
	var kind authoritydomain.PrincipalKind
	switch principal.GetKind() {
	case grantv1.PrincipalKind_PRINCIPAL_KIND_HUMAN:
		kind = authoritydomain.PrincipalHuman
	case grantv1.PrincipalKind_PRINCIPAL_KIND_AGENT:
		kind = authoritydomain.PrincipalAgent
	case grantv1.PrincipalKind_PRINCIPAL_KIND_SYSTEM:
		if allowSystem {
			kind = authoritydomain.PrincipalSystem
		}
	}
	if kind == "" {
		return authoritydomain.Principal{}, servicesvc.InvalArg("principal kind is invalid")
	}
	id, err := id.CanonicalID(principal.GetId(), "principal id")
	if err != nil {
		return authoritydomain.Principal{}, err
	}
	return authoritydomain.Principal{Kind: kind, ID: id, OrganizationID: orgID}, nil
}

func parseScope(scope *grantv1.Scope, orgID string) (authoritydomain.Scope, error) {
	if scope == nil {
		return authoritydomain.Scope{}, servicesvc.InvalArg("scope is required")
	}
	kind, ok := scopeFromProto[scope.GetKind()]
	if !ok {
		return authoritydomain.Scope{}, servicesvc.InvalArg("scope kind is invalid")
	}
	id, err := id.CanonicalID(scope.GetId(), "scope id")
	if err != nil {
		return authoritydomain.Scope{}, err
	}
	if kind == authoritydomain.ScopeOrganization && id != orgID {
		return authoritydomain.Scope{}, connect.NewError(connect.CodePermissionDenied, errors.New("cross-organization scope denied"))
	}
	return authoritydomain.Scope{Kind: kind, ID: id}, nil
}
