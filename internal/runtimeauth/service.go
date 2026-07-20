package runtimeauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"math"
	"time"

	"connectrpc.com/connect"
	runtimev1 "github.com/abcdlsj/sumi/gen/go/sumi/runtime/v1"
	"github.com/abcdlsj/sumi/internal/connectapi"
	"github.com/abcdlsj/sumi/internal/store"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const sessionTTL = 10 * time.Minute

type sessionStore interface {
	CreateAgentRuntimeSession(context.Context, store.CreateAgentRuntimeSessionParams) (store.AgentRuntimeSession, error)
	RenewAgentRuntimeSession(context.Context, store.RenewAgentRuntimeSessionParams) (store.AgentRuntimeSession, error)
	RevokeAgentRuntimeSession(context.Context, store.RevokeAgentRuntimeSessionParams) error
}

type Config struct {
	Now    func() time.Time
	Random io.Reader
}

type Service struct {
	store  sessionStore
	now    func() time.Time
	random io.Reader
}

func NewService(database sessionStore, config Config) *Service {
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Random == nil {
		config.Random = rand.Reader
	}
	return &Service{store: database, now: config.Now, random: config.Random}
}

func (s *Service) CreateAgentRuntimeSession(ctx context.Context, request *connect.Request[runtimev1.CreateAgentRuntimeSessionRequest]) (*connect.Response[runtimev1.CreateAgentRuntimeSessionResponse], error) {
	computerID, agentID, generation, err := sessionBinding(
		request.Msg.GetComputerId(), request.Msg.GetAgentId(), request.Msg.GetPlacementGeneration(),
	)
	if err != nil {
		return nil, err
	}
	if err := registrationKeyValid(request.Msg.GetRegistrationKey()); err != nil {
		return nil, err
	}
	token, err := s.randomToken()
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("generate agent runtime session"))
	}
	now := s.now()
	session, err := s.store.CreateAgentRuntimeSession(ctx, store.CreateAgentRuntimeSessionParams{
		ComputerID: computerID, RegistrationKey: request.Msg.GetRegistrationKey(),
		AgentID: agentID, PlacementGeneration: generation,
		Token: token, Now: now, ExpiresAt: now.Add(sessionTTL),
	})
	if err := createError(err); err != nil {
		return nil, err
	}
	return connect.NewResponse(&runtimev1.CreateAgentRuntimeSessionResponse{
		Session: sessionMessage(session, token),
	}), nil
}

func (s *Service) RenewAgentRuntimeSession(ctx context.Context, request *connect.Request[runtimev1.RenewAgentRuntimeSessionRequest]) (*connect.Response[runtimev1.RenewAgentRuntimeSessionResponse], error) {
	_, proof, err := Subject(ctx)
	if err != nil {
		return nil, err
	}
	computerID, err := connectapi.CanonicalID(request.Msg.GetComputerId(), "computer id")
	if err != nil {
		return nil, err
	}
	if err := registrationKeyValid(request.Msg.GetRegistrationKey()); err != nil {
		return nil, err
	}
	token, err := s.randomToken()
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("generate agent runtime session"))
	}
	now := s.now()
	session, err := s.store.RenewAgentRuntimeSession(ctx, store.RenewAgentRuntimeSessionParams{
		Proof: proof, ComputerID: computerID, RegistrationKey: request.Msg.GetRegistrationKey(),
		Token: token, Now: now, ExpiresAt: now.Add(sessionTTL),
	})
	if err := renewError(err); err != nil {
		return nil, err
	}
	return connect.NewResponse(&runtimev1.RenewAgentRuntimeSessionResponse{
		Session: sessionMessage(session, token),
	}), nil
}

func (s *Service) RevokeAgentRuntimeSession(ctx context.Context, request *connect.Request[runtimev1.RevokeAgentRuntimeSessionRequest]) (*connect.Response[runtimev1.RevokeAgentRuntimeSessionResponse], error) {
	_, proof, err := Subject(ctx)
	if err != nil {
		return nil, err
	}
	computerID, err := connectapi.CanonicalID(request.Msg.GetComputerId(), "computer id")
	if err != nil {
		return nil, err
	}
	if err := registrationKeyValid(request.Msg.GetRegistrationKey()); err != nil {
		return nil, err
	}
	err = s.store.RevokeAgentRuntimeSession(ctx, store.RevokeAgentRuntimeSessionParams{
		Proof: proof, ComputerID: computerID, RegistrationKey: request.Msg.GetRegistrationKey(), Now: s.now(),
	})
	if err := renewError(err); err != nil {
		return nil, err
	}
	return connect.NewResponse(&runtimev1.RevokeAgentRuntimeSessionResponse{}), nil
}

func (s *Service) randomToken() (string, error) {
	payload := make([]byte, 32)
	if _, err := io.ReadFull(s.random, payload); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func sessionBinding(computerValue, agentValue string, generation uint64) (string, string, uint64, error) {
	computerID, err := connectapi.CanonicalID(computerValue, "computer id")
	if err != nil {
		return "", "", 0, err
	}
	agentID, err := connectapi.CanonicalID(agentValue, "agent id")
	if err != nil {
		return "", "", 0, err
	}
	if generation == 0 || generation > math.MaxInt64 {
		return "", "", 0, connect.NewError(connect.CodeInvalidArgument, errors.New("placement generation must be a positive integer"))
	}
	return computerID, agentID, generation, nil
}

func registrationKeyValid(key string) error {
	if key == "" {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("registration key is required"))
	}
	if len(key) > 256 {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("registration key is too long"))
	}
	return nil
}

func createError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrComputerNotFound):
		return connect.NewError(connect.CodeNotFound, errors.New("computer not found"))
	case errors.Is(err, store.ErrRegistrationKeyMismatch):
		return connect.NewError(connect.CodePermissionDenied, errors.New("computer credentials do not match"))
	case errors.Is(err, store.ErrAgentRuntimeBinding):
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("agent runtime binding unavailable"))
	case errors.Is(err, store.ErrAgentRuntimeInvalid):
		return connect.NewError(connect.CodeInvalidArgument, errors.New("agent runtime session invalid"))
	default:
		return connect.NewError(connect.CodeInternal, errors.New("create agent runtime session"))
	}
}

func renewError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrAgentRuntimeUnauthenticated):
		return unauthenticated()
	case errors.Is(err, store.ErrComputerNotFound), errors.Is(err, store.ErrRegistrationKeyMismatch), errors.Is(err, store.ErrAgentRuntimeBinding):
		return connect.NewError(connect.CodePermissionDenied, errors.New("computer credentials do not match agent runtime session"))
	case errors.Is(err, store.ErrAgentRuntimeInvalid):
		return connect.NewError(connect.CodeInvalidArgument, errors.New("agent runtime session invalid"))
	default:
		return connect.NewError(connect.CodeInternal, errors.New("change agent runtime session"))
	}
}

func sessionMessage(session store.AgentRuntimeSession, token string) *runtimev1.AgentRuntimeSession {
	return &runtimev1.AgentRuntimeSession{
		AgentId: session.AgentID, ComputerId: session.ComputerID,
		PlacementGeneration: session.PlacementGeneration,
		Token:               token, ExpiresAt: timestamppb.New(session.ExpiresAt),
	}
}
