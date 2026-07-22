package grant

import (
	"context"

	grantapp "github.com/abcdlsj/sumi/internal/grant/application"
)

type grantStore interface {
	IssueGrant(context.Context, grantapp.IssueCommand) (grantapp.Grant, error)
	RevokeGrant(context.Context, grantapp.RevokeCommand) (grantapp.Grant, error)
	GetGrant(context.Context, grantapp.GetQuery) (grantapp.Grant, error)
	ListGrants(context.Context, grantapp.ListQuery) ([]grantapp.Grant, error)
	CheckPermission(context.Context, grantapp.PermissionQuery) (bool, error)
}
