package application

import (
	"time"

	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
)

type Agent struct {
	ID          string
	Name        string
	Description string
	Driver      string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type CreateCommand struct {
	RequestID   string
	Actor       authoritydomain.Principal
	Name        string
	Description string
	Driver      string
	Now         time.Time
}
