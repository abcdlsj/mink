package computer

import "time"

const connectivityTTL = 30 * time.Second

type Service struct {
	store computerStore
	now   func() time.Time
}

func New(database computerStore) *Service {
	return &Service{store: database, now: time.Now}
}
