package computer

import "time"

const connectivityTTL = 30 * time.Second

type Service struct {
	store computerStore
	now   func() time.Time
}

func New(db computerStore) *Service {
	return &Service{store: db, now: time.Now}
}
