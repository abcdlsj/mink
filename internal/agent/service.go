package agent

import (
	"context"
	"errors"
	"math"
	"net/url"
	"regexp"
	"time"
	"unicode"
	"unicode/utf8"

	"connectrpc.com/connect"
	agentv1 "github.com/abcdlsj/sumi/gen/go/sumi/agent/v1"
	agentapp "github.com/abcdlsj/sumi/internal/agent/application"
	"github.com/abcdlsj/sumi/internal/authority"
	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
	"github.com/abcdlsj/sumi/internal/servicesvc"
	"github.com/abcdlsj/sumi/internal/transport/connectid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var agentHandleRE = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[-_][a-z0-9]+)*$`)

type Service struct {
	store agentStore
	now   func() time.Time
}

func New(db agentStore) *Service {
	return &Service{store: db, now: time.Now}
}

// ── Handlers ─────────────────────────────────────────────────

func (s *Service) CreateAgent(ctx context.Context, req *connect.Request[agentv1.CreateAgentRequest]) (*connect.Response[agentv1.CreateAgentResponse], error) {
	actor, err := authority.Subject(ctx)
	if err != nil {
		return nil, err
	}
	params, err := buildCreateParams(req.Msg, s.now())
	if err != nil {
		return nil, err
	}
	params.Actor = actor
	agent, err := s.store.CreateAgent(ctx, params)
	switch {
	case errors.Is(err, agentapp.ErrRequestConflict):
		return nil, connect.NewError(connect.CodeAlreadyExists, errors.New("request id already exists with different agent data"))
	case errors.Is(err, agentapp.ErrHandleExists):
		return nil, connect.NewError(connect.CodeAlreadyExists, errors.New("agent handle already exists"))
	case errors.Is(err, authoritydomain.ErrPermissionDenied):
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("agent creation denied"))
	case err != nil:
		return nil, servicesvc.ErrInternal
	}
	return connect.NewResponse(&agentv1.CreateAgentResponse{Agent: agentToProto(agent)}), nil
}

func (s *Service) UpdateAgentProfile(ctx context.Context, req *connect.Request[agentv1.UpdateAgentProfileRequest]) (*connect.Response[agentv1.UpdateAgentProfileResponse], error) {
	actor, err := authority.Subject(ctx)
	if err != nil {
		return nil, err
	}
	requestID, err := connectid.CanonicalID(req.Msg.GetRequestId(), "request id")
	if err != nil {
		return nil, err
	}
	agentID, err := connectid.CanonicalID(req.Msg.GetAgentId(), "agent id")
	if err != nil {
		return nil, err
	}
	rev := req.Msg.GetExpectedRevision()
	if rev == 0 || rev > math.MaxInt64 {
		return nil, servicesvc.InvalArg("expected profile revision must be a positive integer")
	}
	profile, err := validateProfile(req.Msg.GetDisplayName(), req.Msg.GetRole(), req.Msg.GetMission(), req.Msg.GetInstructions())
	if err != nil {
		return nil, err
	}
	agent, err := s.store.UpdateAgentProfile(ctx, agentapp.UpdateProfileCommand{
		RequestID: requestID, Actor: actor, AgentID: agentID, ExpectedRevision: rev,
		DisplayName: profile.DisplayName, Role: profile.Role, Mission: profile.Mission,
		Instructions: profile.Instructions, Now: s.now(),
	})
	switch {
	case errors.Is(err, agentapp.ErrNotFound):
		return nil, connect.NewError(connect.CodeNotFound, errors.New("agent not found"))
	case errors.Is(err, agentapp.ErrRequestConflict):
		return nil, connect.NewError(connect.CodeAlreadyExists, errors.New("request id already exists with different profile data"))
	case errors.Is(err, agentapp.ErrRevisionConflict):
		return nil, connect.NewError(connect.CodeAborted, errors.New("agent profile revision changed"))
	case errors.Is(err, authoritydomain.ErrPermissionDenied):
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("agent profile update denied"))
	case err != nil:
		return nil, servicesvc.ErrInternal
	}
	return connect.NewResponse(&agentv1.UpdateAgentProfileResponse{Agent: agentToProto(agent)}), nil
}

func (s *Service) UpdateAgentRuntimeSpec(ctx context.Context, req *connect.Request[agentv1.UpdateAgentRuntimeSpecRequest]) (*connect.Response[agentv1.UpdateAgentRuntimeSpecResponse], error) {
	actor, err := authority.Subject(ctx)
	if err != nil {
		return nil, err
	}
	requestID, err := connectid.CanonicalID(req.Msg.GetRequestId(), "request id")
	if err != nil {
		return nil, err
	}
	agentID, err := connectid.CanonicalID(req.Msg.GetAgentId(), "agent id")
	if err != nil {
		return nil, err
	}
	rev := req.Msg.GetExpectedRevision()
	if rev > math.MaxInt64 {
		return nil, servicesvc.InvalArg("expected runtime spec revision is too large")
	}
	params, err := buildSpecParams(req.Msg)
	if err != nil {
		return nil, err
	}
	params.RequestID = requestID
	params.Actor = actor
	params.AgentID = agentID
	params.ExpectedRevision = rev
	params.Now = s.now()
	spec, err := s.store.UpdateAgentRuntimeSpec(ctx, params)
	switch {
	case errors.Is(err, agentapp.ErrNotFound):
		return nil, connect.NewError(connect.CodeNotFound, errors.New("agent not found"))
	case errors.Is(err, agentapp.ErrRequestConflict):
		return nil, connect.NewError(connect.CodeAlreadyExists, errors.New("request id already exists with different runtime spec data"))
	case errors.Is(err, agentapp.ErrRuntimeSpecRevisionConflict):
		return nil, connect.NewError(connect.CodeAborted, errors.New("agent runtime spec revision changed"))
	case errors.Is(err, authoritydomain.ErrPermissionDenied):
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("agent runtime configuration denied"))
	case err != nil:
		return nil, servicesvc.ErrInternal
	}
	return connect.NewResponse(&agentv1.UpdateAgentRuntimeSpecResponse{RuntimeSpec: specToProto(spec)}), nil
}

func (s *Service) GetAgentRuntimeSpec(ctx context.Context, req *connect.Request[agentv1.GetAgentRuntimeSpecRequest]) (*connect.Response[agentv1.GetAgentRuntimeSpecResponse], error) {
	agentID, err := connectid.CanonicalID(req.Msg.GetAgentId(), "agent id")
	if err != nil {
		return nil, err
	}
	spec, err := s.store.GetAgentRuntimeSpec(ctx, agentID)
	switch {
	case errors.Is(err, agentapp.ErrNotFound):
		return nil, connect.NewError(connect.CodeNotFound, errors.New("agent not found"))
	case errors.Is(err, agentapp.ErrRuntimeSpecMissing):
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("agent runtime spec is not configured"))
	case err != nil:
		return nil, servicesvc.ErrInternal
	}
	return connect.NewResponse(&agentv1.GetAgentRuntimeSpecResponse{RuntimeSpec: specToProto(spec)}), nil
}

func (s *Service) GetAgent(ctx context.Context, req *connect.Request[agentv1.GetAgentRequest]) (*connect.Response[agentv1.GetAgentResponse], error) {
	id, err := connectid.CanonicalID(req.Msg.GetAgentId(), "agent id")
	if err != nil {
		return nil, err
	}
	agent, err := s.store.GetAgent(ctx, id)
	switch {
	case errors.Is(err, agentapp.ErrNotFound):
		return nil, connect.NewError(connect.CodeNotFound, errors.New("agent not found"))
	case err != nil:
		return nil, servicesvc.ErrInternal
	}
	return connect.NewResponse(&agentv1.GetAgentResponse{Agent: agentToProto(agent)}), nil
}

func (s *Service) ListAgents(ctx context.Context, _ *connect.Request[agentv1.ListAgentsRequest]) (*connect.Response[agentv1.ListAgentsResponse], error) {
	agents, err := s.store.ListAgents(ctx)
	if err != nil {
		return nil, servicesvc.ErrInternal
	}
	resp := &agentv1.ListAgentsResponse{Agents: make([]*agentv1.Agent, 0, len(agents))}
	for _, a := range agents {
		resp.Agents = append(resp.Agents, agentToProto(a))
	}
	return connect.NewResponse(resp), nil
}

// ── Params ───────────────────────────────────────────────────

func buildCreateParams(msg *agentv1.CreateAgentRequest, now time.Time) (agentapp.CreateCommand, error) {
	requestID, err := connectid.CanonicalID(msg.GetRequestId(), "request id")
	if err != nil {
		return agentapp.CreateCommand{}, err
	}
	handle := msg.GetHandle()
	if !agentHandleRE.MatchString(handle) || len(handle) > 32 {
		return agentapp.CreateCommand{}, servicesvc.InvalArg("agent handle must contain 1 to 32 lowercase letters, digits, hyphens, or underscores")
	}
	profile, err := validateProfile(msg.GetDisplayName(), msg.GetRole(), msg.GetMission(), msg.GetInstructions())
	if err != nil {
		return agentapp.CreateCommand{}, err
	}
	return agentapp.CreateCommand{
		RequestID: requestID, Handle: handle,
		DisplayName: profile.DisplayName, Role: profile.Role, Mission: profile.Mission,
		Instructions: profile.Instructions, Now: now,
	}, nil
}

func validateProfile(displayName, role, mission, instructions string) (agentapp.Profile, error) {
	type field struct {
		name    string
		value   string
		min, max int
	}
	for _, f := range []field{
		{"display name", displayName, 1, 100},
		{"role", role, 1, 200},
		{"mission", mission, 1, 2000},
		{"instructions", instructions, 0, 20000},
	} {
		if !utf8.ValidString(f.value) {
			return agentapp.Profile{}, servicesvc.InvalArg(f.name + " must be valid UTF-8")
		}
		n := utf8.RuneCountInString(f.value)
		if n < f.min || n > f.max {
			return agentapp.Profile{}, servicesvc.InvalArg(f.name + " length is invalid")
		}
	}
	return agentapp.Profile{DisplayName: displayName, Role: role, Mission: mission, Instructions: instructions}, nil
}

func buildSpecParams(msg *agentv1.UpdateAgentRuntimeSpecRequest) (agentapp.UpdateRuntimeSpecCommand, error) {
	engine, ok := servicesvc.EngineFromProto(msg.GetEngine())
	if !ok {
		return agentapp.UpdateRuntimeSpecCommand{}, servicesvc.InvalArg("runtime engine must be builtin, Codex adapter, or Claude adapter")
	}
	providerProtocol, _ := servicesvc.ProvFromProto(msg.GetProviderProtocol())
	endpoint := msg.GetProviderEndpoint()
	model := msg.GetModel()
	if !utf8.ValidString(model) || utf8.RuneCountInString(model) > 255 {
		return agentapp.UpdateRuntimeSpecCommand{}, servicesvc.InvalArg("runtime model must contain at most 255 characters")
	}
	if engine == agentapp.EngineBuiltin {
		if providerProtocol == "" || model == "" || !validEndpoint(endpoint) {
			return agentapp.UpdateRuntimeSpecCommand{}, servicesvc.InvalArg("builtin runtime requires a supported provider protocol, HTTPS endpoint, and model")
		}
	} else if msg.GetProviderProtocol() != agentv1.ProviderProtocol_PROVIDER_PROTOCOL_UNSPECIFIED || endpoint != "" {
		return agentapp.UpdateRuntimeSpecCommand{}, servicesvc.InvalArg("external adapters cannot contain builtin provider configuration")
	}
	binding := msg.GetCredentialBindingHandle()
	if !validHandle(binding) {
		return agentapp.UpdateRuntimeSpecCommand{}, servicesvc.InvalArg("credential binding handle is invalid")
	}
	if msg.GetSandboxProvider() != agentv1.RuntimeSandboxProvider_RUNTIME_SANDBOX_PROVIDER_TRUSTED_LOCAL {
		return agentapp.UpdateRuntimeSpecCommand{}, servicesvc.InvalArg("runtime sandbox provider must be trusted-local")
	}
	seconds := msg.GetMaxRunDurationSeconds()
	if seconds == 0 || seconds > 3600 {
		return agentapp.UpdateRuntimeSpecCommand{}, servicesvc.InvalArg("max run duration must be between 1 and 3600 seconds")
	}
	outputBytes := msg.GetMaxOutputBytes()
	if outputBytes < 1024 || outputBytes > 64<<20 {
		return agentapp.UpdateRuntimeSpecCommand{}, servicesvc.InvalArg("max output bytes must be between 1024 and 67108864")
	}
	tools := msg.GetToolPolicy()
	if tools == nil {
		return agentapp.UpdateRuntimeSpecCommand{}, servicesvc.InvalArg("runtime tool policy is required")
	}
	return agentapp.UpdateRuntimeSpecCommand{
		Engine: engine, ProviderProtocol: providerProtocol, ProviderEndpoint: endpoint, Model: model,
		CredentialBindingHandle: binding, SandboxProvider: "trusted_local",
		MaxRunDuration: time.Duration(seconds) * time.Second, MaxOutputBytes: outputBytes,
		ToolPolicy: agentapp.ToolPolicy{Message: tools.GetMessage(), Work: tools.GetWork(), Artifact: tools.GetArtifact(), Knowledge: tools.GetKnowledge()},
	}, nil
}

func validEndpoint(v string) bool {
	u, err := url.Parse(v)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && u.RawQuery == "" && u.Fragment == ""
}

func validHandle(v string) bool {
	if !utf8.ValidString(v) || utf8.RuneCountInString(v) < 16 || utf8.RuneCountInString(v) > 255 {
		return false
	}
	for _, r := range v {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// ── Proto converters ─────────────────────────────────────────

func agentToProto(a agentapp.Agent) *agentv1.Agent {
	return &agentv1.Agent{
		Id: a.ID, Handle: a.Handle,
		Profile: &agentv1.AgentProfile{
			AgentId: a.Profile.AgentID, Revision: a.Profile.Revision,
			DisplayName: a.Profile.DisplayName, Role: a.Profile.Role,
			Mission: a.Profile.Mission, Instructions: a.Profile.Instructions,
			CreatedAt: timestamppb.New(a.Profile.CreatedAt),
		},
		CreatedAt: servicesvc.Ts(a.CreatedAt),
		UpdatedAt: servicesvc.Ts(a.UpdatedAt),
	}
}

func specToProto(spec agentapp.RuntimeSpec) *agentv1.AgentRuntimeSpec {
	return &agentv1.AgentRuntimeSpec{
		AgentId: spec.AgentID, Revision: spec.Revision,
		Engine:              servicesvc.EngineToProto(spec.Engine),
		ProviderProtocol:    servicesvc.ProvToProto(spec.ProviderProtocol),
		ProviderEndpoint:    spec.ProviderEndpoint,
		Model:               spec.Model,
		CredentialBindingHandle: spec.CredentialBindingHandle,
		SandboxProvider:         agentv1.RuntimeSandboxProvider_RUNTIME_SANDBOX_PROVIDER_TRUSTED_LOCAL,
		MaxRunDurationSeconds:   uint32(spec.MaxRunDuration / time.Second),
		MaxOutputBytes:          spec.MaxOutputBytes,
		ToolPolicy: &agentv1.RuntimeToolPolicy{
			Message: spec.ToolPolicy.Message, Work: spec.ToolPolicy.Work,
			Artifact: spec.ToolPolicy.Artifact, Knowledge: spec.ToolPolicy.Knowledge,
		},
		CreatedAt: servicesvc.Ts(spec.CreatedAt),
	}
}
