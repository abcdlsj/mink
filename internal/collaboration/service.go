package collaboration

import (
	"time"

	spacev1connect "github.com/abcdlsj/sumi/gen/go/sumi/space/v1/spacev1connect"
)

const (
	defaultMessageLimit = 50
	maxMessageLimit     = 200
	maxMessageBodyRunes = 400_000
)

type Service struct {
	store  collaborationStore
	origin string
	now    func() time.Time
}

var _ spacev1connect.CollaborationServiceHandler = (*Service)(nil)

func New(db collaborationStore, browserOrigin string) *Service {
	return &Service{store: db, origin: browserOrigin, now: time.Now}
}
