package grant

import "time"

type Service struct {
	store grantStore
	now   func() time.Time
}

func New(db grantStore) *Service {
	return &Service{store: db, now: time.Now}
}
