package application

import (
	"time"

	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
)

type Grant struct {
	ID             string
	OrganizationID string
	Subject        authoritydomain.Principal
	Issuer         authoritydomain.Principal
	Capability     authoritydomain.Capability
	Scope          authoritydomain.Scope
	ParentGrantID  string
	ExpiresAt      *time.Time
	RevokedAt      *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type IssueCommand struct {
	RequestID     string
	Actor         authoritydomain.Principal
	Subject       authoritydomain.Principal
	Capability    authoritydomain.Capability
	Scope         authoritydomain.Scope
	ParentGrantID string
	ExpiresAt     *time.Time
	Now           time.Time
}

type RevokeCommand struct {
	RequestID string
	Actor     authoritydomain.Principal
	GrantID   string
	Now       time.Time
}

type GetQuery struct {
	GrantID string
}

type ListQuery struct {
	OrganizationID string
}

type PermissionQuery struct {
	Subject    authoritydomain.Principal
	Capability authoritydomain.Capability
	Scope      authoritydomain.Scope
	Now        time.Time
}
