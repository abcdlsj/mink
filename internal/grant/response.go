package grant

import (
	grantv1 "github.com/abcdlsj/sumi/gen/go/sumi/grant/v1"
	"github.com/abcdlsj/sumi/internal/store"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func grantMessage(value store.Grant) *grantv1.Grant {
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

func principalMessage(value store.Principal) *grantv1.Principal {
	kind := grantv1.PrincipalKind_PRINCIPAL_KIND_UNSPECIFIED
	if value.Kind == "system" {
		kind = grantv1.PrincipalKind_PRINCIPAL_KIND_SYSTEM
	} else if value.Kind == "human" {
		kind = grantv1.PrincipalKind_PRINCIPAL_KIND_HUMAN
	} else if value.Kind == "agent" {
		kind = grantv1.PrincipalKind_PRINCIPAL_KIND_AGENT
	}
	return &grantv1.Principal{Kind: kind, Id: value.ID}
}

func scopeMessage(value store.Scope) *grantv1.Scope {
	kind := grantv1.ScopeKind_SCOPE_KIND_UNSPECIFIED
	if value.Kind == "organization" {
		kind = grantv1.ScopeKind_SCOPE_KIND_ORGANIZATION
	} else if value.Kind == "agent" {
		kind = grantv1.ScopeKind_SCOPE_KIND_AGENT
	} else if value.Kind == "computer" {
		kind = grantv1.ScopeKind_SCOPE_KIND_COMPUTER
	} else if value.Kind == "space" {
		kind = grantv1.ScopeKind_SCOPE_KIND_SPACE
	}
	return &grantv1.Scope{Kind: kind, Id: value.ID}
}
