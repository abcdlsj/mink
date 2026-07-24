package grant

import (
	grantv1 "github.com/abcdlsj/sumi/gen/go/sumi/grant/v1"
	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
	grantapp "github.com/abcdlsj/sumi/internal/grant/application"
	"github.com/abcdlsj/sumi/internal/servicesvc"
)

var principalToProto = map[authoritydomain.PrincipalKind]grantv1.PrincipalKind{
	authoritydomain.PrincipalSystem: grantv1.PrincipalKind_PRINCIPAL_KIND_SYSTEM,
	authoritydomain.PrincipalHuman:  grantv1.PrincipalKind_PRINCIPAL_KIND_HUMAN,
	authoritydomain.PrincipalAgent:  grantv1.PrincipalKind_PRINCIPAL_KIND_AGENT,
}

func grantToProto(g grantapp.Grant) *grantv1.Grant {
	msg := &grantv1.Grant{
		Id: g.ID, OrganizationId: g.OrganizationID,
		Subject:     grantPrincipalToProto(g.Subject),
		Issuer:      grantPrincipalToProto(g.Issuer),
		Capability:  capValue(g.Capability),
		Scope:       grantScopeToProto(g.Scope),
		ParentGrantId: g.ParentGrantID,
		CreatedAt:   servicesvc.Ts(g.CreatedAt),
		UpdatedAt:   servicesvc.Ts(g.UpdatedAt),
	}
	if g.ExpiresAt != nil {
		msg.ExpiresAt = servicesvc.Ts(*g.ExpiresAt)
	}
	if g.RevokedAt != nil {
		msg.RevokedAt = servicesvc.Ts(*g.RevokedAt)
	}
	return msg
}

func grantPrincipalToProto(p authoritydomain.Principal) *grantv1.Principal {
	kind := principalToProto[p.Kind]
	return &grantv1.Principal{Kind: kind, Id: p.ID}
}

func grantScopeToProto(s authoritydomain.Scope) *grantv1.Scope {
	return &grantv1.Scope{
		Kind: scopeToProto[s.Kind],
		Id:   s.ID,
	}
}
