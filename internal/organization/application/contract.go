package application

import (
	"time"

	authorityapp "github.com/abcdlsj/sumi/internal/authority/application"
	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
)

type Organization struct {
	ID               string
	Name             string
	BootstrapHumanID string
	CreatedAt        time.Time
}

type Human struct {
	ID             string
	OrganizationID string
	Name           string
	Role           string
	Status         string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type CreateHumanCommand struct {
	RequestID string
	Actor     authoritydomain.Principal
	Name      string
	Role      string
	Identity  authorityapp.AuthenticationIdentity
	Password  authorityapp.PasswordDigest
	Now       time.Time
}

type SetHumanStatusCommand struct {
	RequestID string
	Actor     authoritydomain.Principal
	HumanID   string
	Status    string
	Now       time.Time
}
