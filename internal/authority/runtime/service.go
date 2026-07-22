package runtime

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
	authorityapp "github.com/abcdlsj/sumi/internal/authority/application"
	computerapp "github.com/abcdlsj/sumi/internal/computer/application"
	"github.com/abcdlsj/sumi/internal/transport/connectid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const sessionTTL = 10 * time.Minute

type sessionStore interface {
	CreateAgentRuntimeSession(context.Context, authorityapp.CreateRuntimeSessionCommand) (authorityapp.RuntimeSession, error)
	RenewAgentRuntimeSession(context.Context, authorityapp.RenewRuntimeSessionCommand) (authorityapp.RuntimeSession, error)
	RevokeAgentRuntimeSession(context.Context, authorityapp.RevokeRuntimeSessionCommand) error
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
	session, err := s.store.CreateAgentRuntimeSession(ctx, authorityapp.CreateRuntimeSessionCommand{
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
	computerID, err := connectid.CanonicalID(request.Msg.GetComputerId(), "computer id")
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
	session, err := s.store.RenewAgentRuntimeSession(ctx, authorityapp.RenewRuntimeSessionCommand{
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
	computerID, err := connectid.CanonicalID(request.Msg.GetComputerId(), "computer id")
	if err != nil {
		return nil, err
	}
	if err := registrationKeyValid(request.Msg.GetRegistrationKey()); err != nil {
		return nil, err
	}
	err = s.store.RevokeAgentRuntimeSession(ctx, authorityapp.RevokeRuntimeSessionCommand{
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
	computerID, err := connectid.CanonicalID(computerValue, "computer id")
	if err != nil {
		return "", "", 0, err
	}
	agentID, err := connectid.CanonicalID(agentValue, "agent id")
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
	case errors.Is(err, computerapp.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, errors.New("computer not found"))
	case errors.Is(err, computerapp.ErrRegistrationKeyMismatch):
		return connect.NewError(connect.CodePermissionDenied, errors.New("computer credentials do not match"))
	case errors.Is(err, authorityapp.ErrRuntimeBinding):
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("agent runtime binding unavailable"))
	case errors.Is(err, authorityapp.ErrRuntimeInvalid):
		return connect.NewError(connect.CodeInvalidArgument, errors.New("agent runtime session invalid"))
	default:
		return connect.NewError(connect.CodeInternal, errors.New("create agent runtime session"))
	}
}

func renewError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, authorityapp.ErrRuntimeUnauthenticated):
		return unauthenticated()
	case errors.Is(err, computerapp.ErrNotFound), errors.Is(err, computerapp.ErrRegistrationKeyMismatch), errors.Is(err, authorityapp.ErrRuntimeBinding):
		return connect.NewError(connect.CodePermissionDenied, errors.New("computer credentials do not match agent runtime session"))
	case errors.Is(err, authorityapp.ErrRuntimeInvalid):
		return connect.NewError(connect.CodeInvalidArgument, errors.New("agent runtime session invalid"))
	default:
		return connect.NewError(connect.CodeInternal, errors.New("change agent runtime session"))
	}
}

func sessionMessage(session authorityapp.RuntimeSession, token string) *runtimev1.AgentRuntimeSession {
	return &runtimev1.AgentRuntimeSession{
		AgentId: session.AgentID, ComputerId: session.ComputerID,
		PlacementGeneration: session.PlacementGeneration,
		Token:               token, ExpiresAt: timestamppb.New(session.ExpiresAt),
	}
}
