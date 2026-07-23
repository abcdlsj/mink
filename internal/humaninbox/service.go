package humaninbox

import (
	"context"
	"errors"
	"net/http"
	"time"

	"connectrpc.com/connect"
	inboxv1 "github.com/abcdlsj/sumi/gen/go/sumi/inbox/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/inbox/v1/inboxv1connect"
	sharedauthentication "github.com/abcdlsj/sumi/internal/authentication"
	"github.com/abcdlsj/sumi/internal/store"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type storeReader interface {
	sharedauthentication.Authenticator
	ListHumanInboxItems(context.Context, store.HumanInboxQuery) ([]store.HumanInboxItem, error)
}

type Service struct {
	store  storeReader
	origin string
	now    func() time.Time
}

var _ inboxv1connect.HumanInboxServiceHandler = (*Service)(nil)

func New(database storeReader, browserOrigin string) *Service {
	return &Service{store: database, origin: browserOrigin, now: time.Now}
}

func (s *Service) ListHumanInboxItems(ctx context.Context, request *connect.Request[inboxv1.ListHumanInboxItemsRequest]) (*connect.Response[inboxv1.ListHumanInboxItemsResponse], error) {
	resolved, err := sharedauthentication.Resolve(ctx, s.store, http.Header(request.Header()), false, s.origin, s.now())
	if err != nil {
		return nil, authenticationError(err)
	}
	human, ok := resolved.Human()
	if !ok || !human.Valid() {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("human session is required"))
	}
	items, err := s.store.ListHumanInboxItems(ctx, store.HumanInboxQuery{Human: human, Limit: request.Msg.GetLimit(), Now: s.now()})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("human inbox projection is unavailable"))
	}
	response := &inboxv1.ListHumanInboxItemsResponse{Items: make([]*inboxv1.HumanInboxItem, 0, len(items))}
	for _, item := range items {
		response.Items = append(response.Items, &inboxv1.HumanInboxItem{
			WorkId: item.WorkID, SpaceId: item.SpaceID, AgentId: item.AgentID, Kind: item.Kind,
			Status: item.Status, ReasonCode: item.ReasonCode, UpdatedAt: timestamppb.New(item.UpdatedAt),
		})
	}
	return connect.NewResponse(response), nil
}

func authenticationError(err error) error {
	if errors.Is(err, sharedauthentication.ErrUnavailable) {
		return connect.NewError(connect.CodeUnavailable, errors.New("human authentication is unavailable"))
	}
	return connect.NewError(connect.CodeUnauthenticated, errors.New("human session is required"))
}
