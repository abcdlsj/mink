package platform

import "context"

type Adapter interface {
	ID() string
	Start(ctx context.Context) error
	Stop() error
}
