package audit

import (
	"context"

	auditapp "github.com/abcdlsj/sumi/internal/audit/application"
	grantapp "github.com/abcdlsj/sumi/internal/grant/application"
)

type auditStore interface {
	CheckPermission(context.Context, grantapp.PermissionQuery) (bool, error)
	ListAuditEvents(context.Context, auditapp.ListQuery) ([]auditapp.Event, error)
}
