package agent

import (
	"context"
	"errors"
	"regexp"
	"time"
	"unicode/utf8"

	"connectrpc.com/connect"
	agentv1 "github.com/abcdlsj/sumi/gen/go/sumi/agent/v1"
	agentapp "github.com/abcdlsj/sumi/internal/agent/application"
	"github.com/abcdlsj/sumi/internal/authority"
	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
	"github.com/abcdlsj/sumi/internal/transport/connectid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var agentName = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[-_][a-z0-9]+)*$`)

type Service struct {
	store agentStore
	now   func() time.Time
}

func New(database agentStore) *Service {
	return &Service{store: database, now: time.Now}
}

func (s *Service) CreateAgent(ctx context.Context, request *connect.Request[agentv1.CreateAgentRequest]) (*connect.Response[agentv1.CreateAgentResponse], error) {
	actor, err := authority.Subject(ctx)
	if err != nil {
		return nil, err
	}
	params, err := createParams(request.Msg, s.now())
	if err != nil {
		return nil, err
	}
	params.Actor = actor
	agent, err := s.store.CreateAgent(ctx, params)
	if errors.Is(err, agentapp.ErrRequestConflict) {
		return nil, connect.NewError(connect.CodeAlreadyExists, errors.New("request id already exists with different agent data"))
	}
	if errors.Is(err, agentapp.ErrNameExists) {
		return nil, connect.NewError(connect.CodeAlreadyExists, errors.New("agent name already exists"))
	}
	if errors.Is(err, authoritydomain.ErrPermissionDenied) {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("agent creation denied"))
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&agentv1.CreateAgentResponse{Agent: agentMessage(agent)}), nil
}

func (s *Service) GetAgent(ctx context.Context, request *connect.Request[agentv1.GetAgentRequest]) (*connect.Response[agentv1.GetAgentResponse], error) {
	id, err := connectid.CanonicalID(request.Msg.GetAgentId(), "agent id")
	if err != nil {
		return nil, err
	}
	agent, err := s.store.GetAgent(ctx, id)
	if errors.Is(err, agentapp.ErrNotFound) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("agent not found"))
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&agentv1.GetAgentResponse{Agent: agentMessage(agent)}), nil
}

func (s *Service) ListAgents(ctx context.Context, _ *connect.Request[agentv1.ListAgentsRequest]) (*connect.Response[agentv1.ListAgentsResponse], error) {
	agents, err := s.store.ListAgents(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	response := &agentv1.ListAgentsResponse{Agents: make([]*agentv1.Agent, 0, len(agents))}
	for _, agent := range agents {
		response.Agents = append(response.Agents, agentMessage(agent))
	}
	return connect.NewResponse(response), nil
}

func createParams(request *agentv1.CreateAgentRequest, now time.Time) (agentapp.CreateCommand, error) {
	requestID, err := connectid.CanonicalID(request.GetRequestId(), "request id")
	if err != nil {
		return agentapp.CreateCommand{}, err
	}
	if !agentName.MatchString(request.GetName()) || len(request.GetName()) > 32 {
		return agentapp.CreateCommand{}, connect.NewError(connect.CodeInvalidArgument, errors.New("agent name must be a lowercase handle of 1 to 32 characters"))
	}
	if !utf8.ValidString(request.GetDescription()) || utf8.RuneCountInString(request.GetDescription()) > 1000 {
		return agentapp.CreateCommand{}, connect.NewError(connect.CodeInvalidArgument, errors.New("agent description must contain at most 1000 characters"))
	}
	driver, ok := driverName(request.GetDriver())
	if !ok {
		return agentapp.CreateCommand{}, connect.NewError(connect.CodeInvalidArgument, errors.New("agent driver must be native, codex, or claude"))
	}
	return agentapp.CreateCommand{
		RequestID:   requestID,
		Name:        request.GetName(),
		Description: request.GetDescription(),
		Driver:      driver,
		Now:         now,
	}, nil
}

func driverName(driver agentv1.Driver) (string, bool) {
	switch driver {
	case agentv1.Driver_DRIVER_NATIVE:
		return "native", true
	case agentv1.Driver_DRIVER_CODEX:
		return "codex", true
	case agentv1.Driver_DRIVER_CLAUDE:
		return "claude", true
	default:
		return "", false
	}
}

func agentMessage(agent agentapp.Agent) *agentv1.Agent {
	driver := agentv1.Driver_DRIVER_UNSPECIFIED
	if agent.Driver == "native" {
		driver = agentv1.Driver_DRIVER_NATIVE
	} else if agent.Driver == "codex" {
		driver = agentv1.Driver_DRIVER_CODEX
	} else if agent.Driver == "claude" {
		driver = agentv1.Driver_DRIVER_CLAUDE
	}
	return &agentv1.Agent{
		Id:          agent.ID,
		Name:        agent.Name,
		Description: agent.Description,
		Driver:      driver,
		CreatedAt:   timestamppb.New(agent.CreatedAt),
		UpdatedAt:   timestamppb.New(agent.UpdatedAt),
	}
}
