package grant

import (
	grantv1 "github.com/abcdlsj/sumi/gen/go/sumi/grant/v1"
	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
	grantapp "github.com/abcdlsj/sumi/internal/grant/application"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func grantMessage(value grantapp.Grant) *grantv1.Grant {
	message := &grantv1.Grant{
		Id: value.ID, OrganizationId: value.OrganizationID, Subject: principalMessage(value.Subject), Issuer: principalMessage(value.Issuer),
		Capability: capabilityValue(value.Capability), Scope: scopeMessage(value.Scope), ParentGrantId: value.ParentGrantID,
		CreatedAt: timestamppb.New(value.CreatedAt), UpdatedAt: timestamppb.New(value.UpdatedAt),
	}
	if value.ExpiresAt != nil {
		message.ExpiresAt = timestamppb.New(*value.ExpiresAt)
	}
	if value.RevokedAt != nil {
		message.RevokedAt = timestamppb.New(*value.RevokedAt)
	}
	return message
}

func principalMessage(value authoritydomain.Principal) *grantv1.Principal {
	kind := grantv1.PrincipalKind_PRINCIPAL_KIND_UNSPECIFIED
	if value.Kind == authoritydomain.PrincipalSystem {
		kind = grantv1.PrincipalKind_PRINCIPAL_KIND_SYSTEM
	} else if value.Kind == authoritydomain.PrincipalHuman {
		kind = grantv1.PrincipalKind_PRINCIPAL_KIND_HUMAN
	} else if value.Kind == authoritydomain.PrincipalAgent {
		kind = grantv1.PrincipalKind_PRINCIPAL_KIND_AGENT
	}
	return &grantv1.Principal{Kind: kind, Id: value.ID}
}

func scopeMessage(value authoritydomain.Scope) *grantv1.Scope {
	kind := grantv1.ScopeKind_SCOPE_KIND_UNSPECIFIED
	if value.Kind == authoritydomain.ScopeOrganization {
		kind = grantv1.ScopeKind_SCOPE_KIND_ORGANIZATION
	} else if value.Kind == authoritydomain.ScopeAgent {
		kind = grantv1.ScopeKind_SCOPE_KIND_AGENT
	} else if value.Kind == authoritydomain.ScopeComputer {
		kind = grantv1.ScopeKind_SCOPE_KIND_COMPUTER
	} else if value.Kind == authoritydomain.ScopeSpace {
		kind = grantv1.ScopeKind_SCOPE_KIND_SPACE
	}
	return &grantv1.Scope{Kind: kind, Id: value.ID}
}
