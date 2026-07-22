package organization

import (
	"context"

	"github.com/abcdlsj/sumi/internal/store"
)

type organizationStore interface {
	GetOrganization(context.Context) (store.Organization, error)
	CreateHuman(context.Context, store.CreateHumanParams) (store.Human, error)
	GetHuman(context.Context, string) (store.Human, error)
	ListHumans(context.Context, string) ([]store.Human, error)
	SetHumanStatus(context.Context, store.SetHumanStatusParams) (store.Human, error)
}
