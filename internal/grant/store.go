package grant

import (
	"context"

	"github.com/abcdlsj/sumi/internal/store"
)

type grantStore interface {
	IssueGrant(context.Context, store.IssueGrantParams) (store.Grant, error)
	RevokeGrant(context.Context, store.RevokeGrantParams) (store.Grant, error)
	GetGrant(context.Context, string) (store.Grant, error)
	ListGrants(context.Context, string) ([]store.Grant, error)
	CheckPermission(context.Context, store.CheckPermissionParams) (bool, error)
}
