package placement

import (
	"time"
)

type Service struct {
	store placementStore
	now   func() time.Time
}

func New(db placementStore) *Service {
	return &Service{store: db, now: time.Now}
}
