package organization

import (
	"context"

	organizationapp "github.com/abcdlsj/sumi/internal/organization/application"
)

type organizationStore interface {
	GetOrganization(context.Context) (organizationapp.Organization, error)
	CreateHuman(context.Context, organizationapp.CreateHumanCommand) (organizationapp.Human, error)
	GetHuman(context.Context, string) (organizationapp.Human, error)
	ListHumans(context.Context, string) ([]organizationapp.Human, error)
	SetHumanStatus(context.Context, organizationapp.SetHumanStatusCommand) (organizationapp.Human, error)
}
