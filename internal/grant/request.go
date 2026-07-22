package grant

import (
	"errors"

	"connectrpc.com/connect"
	grantv1 "github.com/abcdlsj/sumi/gen/go/sumi/grant/v1"
	"github.com/abcdlsj/sumi/internal/store"
	"github.com/abcdlsj/sumi/internal/transport/connectid"
)

func principalParams(principal *grantv1.Principal, organizationID string, allowSystem bool) (store.Principal, error) {
	if principal == nil {
		return store.Principal{}, connect.NewError(connect.CodeInvalidArgument, errors.New("principal is required"))
	}
	kind := ""
	if principal.GetKind() == grantv1.PrincipalKind_PRINCIPAL_KIND_HUMAN {
		kind = "human"
	} else if principal.GetKind() == grantv1.PrincipalKind_PRINCIPAL_KIND_AGENT {
		kind = "agent"
	} else if allowSystem && principal.GetKind() == grantv1.PrincipalKind_PRINCIPAL_KIND_SYSTEM {
		kind = "system"
	}
	if kind == "" {
		return store.Principal{}, connect.NewError(connect.CodeInvalidArgument, errors.New("principal kind is invalid"))
	}
	id, err := connectid.CanonicalID(principal.GetId(), "principal id")
	if err != nil {
		return store.Principal{}, err
	}
	return store.Principal{Kind: kind, ID: id, OrganizationID: organizationID}, nil
}

func scopeParams(scope *grantv1.Scope, organizationID string) (store.Scope, error) {
	if scope == nil {
		return store.Scope{}, connect.NewError(connect.CodeInvalidArgument, errors.New("scope is required"))
	}
	kind := ""
	switch scope.GetKind() {
	case grantv1.ScopeKind_SCOPE_KIND_ORGANIZATION:
		kind = "organization"
	case grantv1.ScopeKind_SCOPE_KIND_AGENT:
		kind = "agent"
	case grantv1.ScopeKind_SCOPE_KIND_COMPUTER:
		kind = "computer"
	case grantv1.ScopeKind_SCOPE_KIND_SPACE:
		kind = "space"
	}
	if kind == "" {
		return store.Scope{}, connect.NewError(connect.CodeInvalidArgument, errors.New("scope kind is invalid"))
	}
	id, err := connectid.CanonicalID(scope.GetId(), "scope id")
	if err != nil {
		return store.Scope{}, err
	}
	if kind == "organization" && id != organizationID {
		return store.Scope{}, connect.NewError(connect.CodePermissionDenied, errors.New("cross-organization scope denied"))
	}
	return store.Scope{Kind: kind, ID: id}, nil
}

func capabilityName(value grantv1.Capability) (string, bool) {
	names := map[grantv1.Capability]string{
		grantv1.Capability_CAPABILITY_ORGANIZATION_ADMIN:   store.CapabilityOrganizationAdmin,
		grantv1.Capability_CAPABILITY_HUMAN_CREATE:         store.CapabilityHumanCreate,
		grantv1.Capability_CAPABILITY_GRANT_ISSUE:          store.CapabilityGrantIssue,
		grantv1.Capability_CAPABILITY_GRANT_REVOKE:         store.CapabilityGrantRevoke,
		grantv1.Capability_CAPABILITY_AUDIT_READ:           store.CapabilityAuditRead,
		grantv1.Capability_CAPABILITY_AGENT_CREATE:         store.CapabilityAgentCreate,
		grantv1.Capability_CAPABILITY_AGENT_PLACE:          store.CapabilityAgentPlace,
		grantv1.Capability_CAPABILITY_SPACE_CREATE:         store.CapabilitySpaceCreate,
		grantv1.Capability_CAPABILITY_SPACE_READ:           store.CapabilitySpaceRead,
		grantv1.Capability_CAPABILITY_SPACE_MEMBERS_MANAGE: store.CapabilitySpaceMembers,
		grantv1.Capability_CAPABILITY_SPACE_ARCHIVE:        store.CapabilitySpaceArchive,
		grantv1.Capability_CAPABILITY_MESSAGE_SEND:         store.CapabilityMessageSend,
		grantv1.Capability_CAPABILITY_RUN_EXECUTE:          store.CapabilityRunExecute,
		grantv1.Capability_CAPABILITY_COMPUTER_PAIR:        store.CapabilityComputerPair,
	}
	name, ok := names[value]
	return name, ok
}

func capabilityValue(value string) grantv1.Capability {
	for enum, name := range map[grantv1.Capability]string{
		grantv1.Capability_CAPABILITY_ORGANIZATION_ADMIN:   store.CapabilityOrganizationAdmin,
		grantv1.Capability_CAPABILITY_HUMAN_CREATE:         store.CapabilityHumanCreate,
		grantv1.Capability_CAPABILITY_GRANT_ISSUE:          store.CapabilityGrantIssue,
		grantv1.Capability_CAPABILITY_GRANT_REVOKE:         store.CapabilityGrantRevoke,
		grantv1.Capability_CAPABILITY_AUDIT_READ:           store.CapabilityAuditRead,
		grantv1.Capability_CAPABILITY_AGENT_CREATE:         store.CapabilityAgentCreate,
		grantv1.Capability_CAPABILITY_AGENT_PLACE:          store.CapabilityAgentPlace,
		grantv1.Capability_CAPABILITY_SPACE_CREATE:         store.CapabilitySpaceCreate,
		grantv1.Capability_CAPABILITY_SPACE_READ:           store.CapabilitySpaceRead,
		grantv1.Capability_CAPABILITY_SPACE_MEMBERS_MANAGE: store.CapabilitySpaceMembers,
		grantv1.Capability_CAPABILITY_SPACE_ARCHIVE:        store.CapabilitySpaceArchive,
		grantv1.Capability_CAPABILITY_MESSAGE_SEND:         store.CapabilityMessageSend,
		grantv1.Capability_CAPABILITY_RUN_EXECUTE:          store.CapabilityRunExecute,
		grantv1.Capability_CAPABILITY_COMPUTER_PAIR:        store.CapabilityComputerPair,
	} {
		if name == value {
			return enum
		}
	}
	return grantv1.Capability_CAPABILITY_UNSPECIFIED
}
