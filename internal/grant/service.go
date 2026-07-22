package grant

import "time"

type Service struct {
	store grantStore
	now   func() time.Time
}

func New(database grantStore) *Service {
	return &Service{store: database, now: time.Now}
}
