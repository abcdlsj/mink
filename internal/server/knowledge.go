package server

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	knowledgev1 "github.com/abcdlsj/sumi/gen/go/sumi/knowledge/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/knowledge/v1/knowledgev1connect"
	sharedauthentication "github.com/abcdlsj/sumi/internal/authentication"
	"github.com/abcdlsj/sumi/internal/store"
)

type knowledgeStore interface {
	sharedauthentication.Authenticator
	SearchKnowledge(context.Context, store.KnowledgeSearchParams) (store.KnowledgeSearchOutput, error)
}

type knowledgeService struct {
	store  knowledgeStore
	origin string
	now    func() time.Time
}

var _ knowledgev1connect.KnowledgeServiceHandler = (*knowledgeService)(nil)

func newKnowledgeService(database knowledgeStore, origin string) *knowledgeService {
	return &knowledgeService{store: database, origin: origin, now: time.Now}
}

func (s *knowledgeService) SearchKnowledge(ctx context.Context, request *connect.Request[knowledgev1.SearchKnowledgeRequest]) (*connect.Response[knowledgev1.SearchKnowledgeResponse], error) {
	now := s.now()
	authentication, err := sharedauthentication.Resolve(ctx, s.store, request.Header(), false, s.origin, now)
	if err != nil {
		return nil, knowledgeAuthenticationError(err)
	}
	params := store.KnowledgeSearchParams{
		Query: request.Msg.GetQuery(), Limit: request.Msg.GetLimit(), Now: now,
	}
	if human, ok := authentication.Human(); ok {
		params.Human = human
	} else if agent, ok := authentication.Agent(); ok {
		params.Agent = agent
	} else {
		return nil, knowledgeInternalError()
	}
	output, err := s.store.SearchKnowledge(ctx, params)
	if err != nil {
		return nil, knowledgeServiceError(err)
	}
	response, err := knowledgeResponse(output)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(response), nil
}

func knowledgeAuthenticationError(err error) error {
	switch {
	case errors.Is(err, sharedauthentication.ErrUnauthenticated):
		return connect.NewError(connect.CodeUnauthenticated, errors.New("knowledge authentication invalid"))
	default:
		return knowledgeInternalError()
	}
}

func knowledgeServiceError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.Canceled):
		return connect.NewError(connect.CodeCanceled, errors.New("knowledge search canceled"))
	case errors.Is(err, context.DeadlineExceeded):
		return connect.NewError(connect.CodeDeadlineExceeded, errors.New("knowledge search deadline exceeded"))
	case errors.Is(err, store.ErrKnowledgeSearchInvalid):
		return connect.NewError(connect.CodeInvalidArgument, errors.New("knowledge search input is invalid"))
	case errors.Is(err, store.ErrKnowledgeSearchUnauthenticated):
		return connect.NewError(connect.CodeUnauthenticated, errors.New("knowledge authentication invalid"))
	default:
		return knowledgeInternalError()
	}
}

func knowledgeInternalError() error {
	return connect.NewError(connect.CodeInternal, errors.New("knowledge service unavailable"))
}

func knowledgeResponse(output store.KnowledgeSearchOutput) (*knowledgev1.SearchKnowledgeResponse, error) {
	status, err := knowledgeStatus(output.Status)
	if err != nil {
		return nil, err
	}
	results := make([]*knowledgev1.KnowledgeResult, 0, len(output.Results))
	for _, result := range output.Results {
		citation, err := knowledgeCitation(result.Source)
		if err != nil {
			return nil, err
		}
		results = append(results, &knowledgev1.KnowledgeResult{Citation: citation, Snippet: result.Snippet})
	}
	return &knowledgev1.SearchKnowledgeResponse{Results: results, Status: status}, nil
}

func knowledgeCitation(source store.KnowledgeSource) (*knowledgev1.KnowledgeCitation, error) {
	switch source.Kind {
	case store.KnowledgeSourceMessage:
		return &knowledgev1.KnowledgeCitation{Source: &knowledgev1.KnowledgeCitation_Message{
			Message: &knowledgev1.MessageCitation{MessageId: source.ID},
		}}, nil
	case store.KnowledgeSourceWork:
		return &knowledgev1.KnowledgeCitation{Source: &knowledgev1.KnowledgeCitation_Work{
			Work: &knowledgev1.WorkCitation{WorkId: source.ID},
		}}, nil
	case store.KnowledgeSourceArtifactVersion:
		return &knowledgev1.KnowledgeCitation{Source: &knowledgev1.KnowledgeCitation_ArtifactVersion{
			ArtifactVersion: &knowledgev1.ArtifactVersionCitation{ArtifactId: source.ID, Version: source.Version},
		}}, nil
	default:
		return nil, knowledgeInternalError()
	}
}

func knowledgeStatus(status string) (knowledgev1.KnowledgeIndexStatus, error) {
	switch status {
	case store.KnowledgeIndexReady:
		return knowledgev1.KnowledgeIndexStatus_KNOWLEDGE_INDEX_STATUS_READY, nil
	case store.KnowledgeIndexDegraded:
		return knowledgev1.KnowledgeIndexStatus_KNOWLEDGE_INDEX_STATUS_DEGRADED, nil
	default:
		return knowledgev1.KnowledgeIndexStatus_KNOWLEDGE_INDEX_STATUS_UNSPECIFIED, knowledgeInternalError()
	}
}
