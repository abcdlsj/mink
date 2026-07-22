package audit

import (
	"context"

	"github.com/abcdlsj/sumi/internal/store"
)

type auditStore interface {
	CheckPermission(context.Context, store.CheckPermissionParams) (bool, error)
	ListAuditEvents(context.Context, store.ListAuditEventsParams) ([]store.AuditEvent, error)
}
