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
	"github.com/abcdlsj/sumi/internal/transport/connectid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var agentHandle = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[-_][a-z0-9]+)*$`)

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
	if errors.Is(err, agentapp.ErrHandleExists) {
		return nil, connect.NewError(connect.CodeAlreadyExists, errors.New("agent handle already exists"))
	}
	if errors.Is(err, authoritydomain.ErrPermissionDenied) {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("agent creation denied"))
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&agentv1.CreateAgentResponse{Agent: agentMessage(agent)}), nil
}

func (s *Service) UpdateAgentProfile(ctx context.Context, request *connect.Request[agentv1.UpdateAgentProfileRequest]) (*connect.Response[agentv1.UpdateAgentProfileResponse], error) {
	actor, err := authority.Subject(ctx)
	if err != nil {
		return nil, err
	}
	requestID, err := connectid.CanonicalID(request.Msg.GetRequestId(), "request id")
	if err != nil {
		return nil, err
	}
	agentID, err := connectid.CanonicalID(request.Msg.GetAgentId(), "agent id")
	if err != nil {
		return nil, err
	}
	if request.Msg.GetExpectedRevision() == 0 || request.Msg.GetExpectedRevision() > math.MaxInt64 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("expected profile revision must be a positive integer"))
	}
	profile, err := profileFields(request.Msg.GetDisplayName(), request.Msg.GetRole(), request.Msg.GetMission(), request.Msg.GetInstructions())
	if err != nil {
		return nil, err
	}
	agent, err := s.store.UpdateAgentProfile(ctx, agentapp.UpdateProfileCommand{
		RequestID: requestID, Actor: actor, AgentID: agentID, ExpectedRevision: request.Msg.GetExpectedRevision(),
		DisplayName: profile.DisplayName, Role: profile.Role, Mission: profile.Mission, Instructions: profile.Instructions, Now: s.now(),
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
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&agentv1.UpdateAgentProfileResponse{Agent: agentMessage(agent)}), nil
}

func (s *Service) UpdateAgentRuntimeSpec(ctx context.Context, request *connect.Request[agentv1.UpdateAgentRuntimeSpecRequest]) (*connect.Response[agentv1.UpdateAgentRuntimeSpecResponse], error) {
	actor, err := authority.Subject(ctx)
	if err != nil {
		return nil, err
	}
	requestID, err := connectid.CanonicalID(request.Msg.GetRequestId(), "request id")
	if err != nil {
		return nil, err
	}
	agentID, err := connectid.CanonicalID(request.Msg.GetAgentId(), "agent id")
	if err != nil {
		return nil, err
	}
	if request.Msg.GetExpectedRevision() > math.MaxInt64 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("expected runtime spec revision is too large"))
	}
	params, err := runtimeSpecParams(request.Msg)
	if err != nil {
		return nil, err
	}
	params.RequestID = requestID
	params.Actor = actor
	params.AgentID = agentID
	params.ExpectedRevision = request.Msg.GetExpectedRevision()
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
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&agentv1.UpdateAgentRuntimeSpecResponse{RuntimeSpec: runtimeSpecMessage(spec)}), nil
}

func (s *Service) GetAgentRuntimeSpec(ctx context.Context, request *connect.Request[agentv1.GetAgentRuntimeSpecRequest]) (*connect.Response[agentv1.GetAgentRuntimeSpecResponse], error) {
	agentID, err := connectid.CanonicalID(request.Msg.GetAgentId(), "agent id")
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
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&agentv1.GetAgentRuntimeSpecResponse{RuntimeSpec: runtimeSpecMessage(spec)}), nil
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
	if !agentHandle.MatchString(request.GetHandle()) || len(request.GetHandle()) > 32 {
		return agentapp.CreateCommand{}, connect.NewError(connect.CodeInvalidArgument, errors.New("agent handle must contain 1 to 32 lowercase letters, digits, hyphens, or underscores"))
	}
	profile, err := profileFields(request.GetDisplayName(), request.GetRole(), request.GetMission(), request.GetInstructions())
	if err != nil {
		return agentapp.CreateCommand{}, err
	}
	return agentapp.CreateCommand{
		RequestID: requestID, Handle: request.GetHandle(), DisplayName: profile.DisplayName,
		Role: profile.Role, Mission: profile.Mission, Instructions: profile.Instructions, Now: now,
	}, nil
}

func profileFields(displayName, role, mission, instructions string) (agentapp.Profile, error) {
	fields := []struct {
		name    string
		value   string
		minimum int
		maximum int
	}{
		{"display name", displayName, 1, 100},
		{"role", role, 1, 200},
		{"mission", mission, 1, 2000},
		{"instructions", instructions, 0, 20000},
	}
	for _, field := range fields {
		if !utf8.ValidString(field.value) {
			return agentapp.Profile{}, connect.NewError(connect.CodeInvalidArgument, errors.New(field.name+" must be valid UTF-8"))
		}
		length := utf8.RuneCountInString(field.value)
		if length < field.minimum || length > field.maximum {
			return agentapp.Profile{}, connect.NewError(connect.CodeInvalidArgument, errors.New(field.name+" length is invalid"))
		}
	}
	return agentapp.Profile{DisplayName: displayName, Role: role, Mission: mission, Instructions: instructions}, nil
}

func runtimeSpecParams(request *agentv1.UpdateAgentRuntimeSpecRequest) (agentapp.UpdateRuntimeSpecCommand, error) {
	engine, ok := engineKind(request.GetEngine())
	if !ok {
		return agentapp.UpdateRuntimeSpecCommand{}, connect.NewError(connect.CodeInvalidArgument, errors.New("runtime engine must be builtin, Codex adapter, or Claude adapter"))
	}
	providerProtocol, providerKnown := providerProtocolName(request.GetProviderProtocol())
	endpoint := request.GetProviderEndpoint()
	model := request.GetModel()
	if !utf8.ValidString(model) || utf8.RuneCountInString(model) > 255 {
		return agentapp.UpdateRuntimeSpecCommand{}, connect.NewError(connect.CodeInvalidArgument, errors.New("runtime model must contain at most 255 characters"))
	}
	if engine == agentapp.EngineBuiltin {
		if !providerKnown || providerProtocol == "" || model == "" || !validProviderEndpoint(endpoint) {
			return agentapp.UpdateRuntimeSpecCommand{}, connect.NewError(connect.CodeInvalidArgument, errors.New("builtin runtime requires a supported provider protocol, HTTPS endpoint, and model"))
		}
	} else if request.GetProviderProtocol() != agentv1.ProviderProtocol_PROVIDER_PROTOCOL_UNSPECIFIED || endpoint != "" {
		return agentapp.UpdateRuntimeSpecCommand{}, connect.NewError(connect.CodeInvalidArgument, errors.New("external adapters cannot contain builtin provider configuration"))
	}
	binding := request.GetCredentialBindingHandle()
	if !validOpaqueHandle(binding) {
		return agentapp.UpdateRuntimeSpecCommand{}, connect.NewError(connect.CodeInvalidArgument, errors.New("credential binding handle is invalid"))
	}
	if request.GetSandboxProvider() != agentv1.RuntimeSandboxProvider_RUNTIME_SANDBOX_PROVIDER_TRUSTED_LOCAL {
		return agentapp.UpdateRuntimeSpecCommand{}, connect.NewError(connect.CodeInvalidArgument, errors.New("runtime sandbox provider must be trusted-local"))
	}
	seconds := request.GetMaxRunDurationSeconds()
	if seconds == 0 || seconds > 3600 {
		return agentapp.UpdateRuntimeSpecCommand{}, connect.NewError(connect.CodeInvalidArgument, errors.New("max run duration must be between 1 and 3600 seconds"))
	}
	outputBytes := request.GetMaxOutputBytes()
	if outputBytes < 1024 || outputBytes > 64<<20 {
		return agentapp.UpdateRuntimeSpecCommand{}, connect.NewError(connect.CodeInvalidArgument, errors.New("max output bytes must be between 1024 and 67108864"))
	}
	tools := request.GetToolPolicy()
	if tools == nil {
		return agentapp.UpdateRuntimeSpecCommand{}, connect.NewError(connect.CodeInvalidArgument, errors.New("runtime tool policy is required"))
	}
	return agentapp.UpdateRuntimeSpecCommand{
		Engine: engine, ProviderProtocol: providerProtocol, ProviderEndpoint: endpoint, Model: model,
		CredentialBindingHandle: binding, SandboxProvider: "trusted_local",
		MaxRunDuration: time.Duration(seconds) * time.Second, MaxOutputBytes: outputBytes,
		ToolPolicy: agentapp.ToolPolicy{Message: tools.GetMessage(), Work: tools.GetWork(), Artifact: tools.GetArtifact(), Knowledge: tools.GetKnowledge()},
	}, nil
}

func engineKind(value agentv1.EngineKind) (agentapp.EngineKind, bool) {
	switch value {
	case agentv1.EngineKind_ENGINE_KIND_BUILTIN:
		return agentapp.EngineBuiltin, true
	case agentv1.EngineKind_ENGINE_KIND_CODEX_ADAPTER:
		return agentapp.EngineCodexAdapter, true
	case agentv1.EngineKind_ENGINE_KIND_CLAUDE_ADAPTER:
		return agentapp.EngineClaudeAdapter, true
	default:
		return "", false
	}
}

func providerProtocolName(value agentv1.ProviderProtocol) (agentapp.ProviderProtocol, bool) {
	switch value {
	case agentv1.ProviderProtocol_PROVIDER_PROTOCOL_UNSPECIFIED:
		return "", true
	case agentv1.ProviderProtocol_PROVIDER_PROTOCOL_OPENAI_RESPONSES:
		return agentapp.ProviderOpenAIResponses, true
	case agentv1.ProviderProtocol_PROVIDER_PROTOCOL_ANTHROPIC_MESSAGES:
		return agentapp.ProviderAnthropicMessages, true
	default:
		return "", false
	}
}

func validProviderEndpoint(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == ""
}

func validOpaqueHandle(value string) bool {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) < 16 || utf8.RuneCountInString(value) > 255 {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func agentMessage(agent agentapp.Agent) *agentv1.Agent {
	return &agentv1.Agent{
		Id: agent.ID, Handle: agent.Handle, Profile: &agentv1.AgentProfile{
			AgentId: agent.Profile.AgentID, Revision: agent.Profile.Revision, DisplayName: agent.Profile.DisplayName,
			Role: agent.Profile.Role, Mission: agent.Profile.Mission, Instructions: agent.Profile.Instructions,
			CreatedAt: timestamppb.New(agent.Profile.CreatedAt),
		},
		CreatedAt: timestamppb.New(agent.CreatedAt), UpdatedAt: timestamppb.New(agent.UpdatedAt),
	}
}

func runtimeSpecMessage(spec agentapp.RuntimeSpec) *agentv1.AgentRuntimeSpec {
	engine := agentv1.EngineKind_ENGINE_KIND_UNSPECIFIED
	switch spec.Engine {
	case agentapp.EngineBuiltin:
		engine = agentv1.EngineKind_ENGINE_KIND_BUILTIN
	case agentapp.EngineCodexAdapter:
		engine = agentv1.EngineKind_ENGINE_KIND_CODEX_ADAPTER
	case agentapp.EngineClaudeAdapter:
		engine = agentv1.EngineKind_ENGINE_KIND_CLAUDE_ADAPTER
	}
	protocol := agentv1.ProviderProtocol_PROVIDER_PROTOCOL_UNSPECIFIED
	switch spec.ProviderProtocol {
	case agentapp.ProviderOpenAIResponses:
		protocol = agentv1.ProviderProtocol_PROVIDER_PROTOCOL_OPENAI_RESPONSES
	case agentapp.ProviderAnthropicMessages:
		protocol = agentv1.ProviderProtocol_PROVIDER_PROTOCOL_ANTHROPIC_MESSAGES
	}
	return &agentv1.AgentRuntimeSpec{
		AgentId: spec.AgentID, Revision: spec.Revision, Engine: engine, ProviderProtocol: protocol,
		ProviderEndpoint: spec.ProviderEndpoint, Model: spec.Model, CredentialBindingHandle: spec.CredentialBindingHandle,
		SandboxProvider:       agentv1.RuntimeSandboxProvider_RUNTIME_SANDBOX_PROVIDER_TRUSTED_LOCAL,
		MaxRunDurationSeconds: uint32(spec.MaxRunDuration / time.Second), MaxOutputBytes: spec.MaxOutputBytes,
		ToolPolicy: &agentv1.RuntimeToolPolicy{
			Message: spec.ToolPolicy.Message, Work: spec.ToolPolicy.Work,
			Artifact: spec.ToolPolicy.Artifact, Knowledge: spec.ToolPolicy.Knowledge,
		},
		CreatedAt: timestamppb.New(spec.CreatedAt),
	}
}
