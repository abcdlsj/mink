package workattention

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
	ListWorkAttentionItems(context.Context, store.WorkAttentionQuery) ([]store.WorkAttentionItem, error)
}

type Service struct {
	store  storeReader
	origin string
	now    func() time.Time
}

var _ inboxv1connect.WorkAttentionServiceHandler = (*Service)(nil)

func New(database storeReader, browserOrigin string) *Service {
	return &Service{store: database, origin: browserOrigin, now: time.Now}
}

func (s *Service) ListWorkAttentionItems(ctx context.Context, request *connect.Request[inboxv1.ListWorkAttentionItemsRequest]) (*connect.Response[inboxv1.ListWorkAttentionItemsResponse], error) {
	resolved, err := sharedauthentication.Resolve(ctx, s.store, http.Header(request.Header()), false, s.origin, s.now())
	if err != nil {
		return nil, authenticationError(err)
	}
	human, ok := resolved.Human()
	if !ok || !human.Valid() {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("human session is required"))
	}
	items, err := s.store.ListWorkAttentionItems(ctx, store.WorkAttentionQuery{Human: human, Limit: request.Msg.GetLimit(), Now: s.now()})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("work attention projection is unavailable"))
	}
	response := &inboxv1.ListWorkAttentionItemsResponse{Items: make([]*inboxv1.WorkAttentionItem, 0, len(items))}
	for _, item := range items {
		response.Items = append(response.Items, &inboxv1.WorkAttentionItem{
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
