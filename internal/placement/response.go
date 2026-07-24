package placement

import (
	"time"

	agentv1 "github.com/abcdlsj/sumi/gen/go/sumi/agent/v1"
	placementv1 "github.com/abcdlsj/sumi/gen/go/sumi/placement/v1"
	agentapp "github.com/abcdlsj/sumi/internal/agent/application"
	placementapp "github.com/abcdlsj/sumi/internal/placement/application"
	placementdomain "github.com/abcdlsj/sumi/internal/placement/domain"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func placementMessages(placements []placementapp.Placement) []*placementv1.AgentPlacement {
	messages := make([]*placementv1.AgentPlacement, 0, len(placements))
	for _, placement := range placements {
		messages = append(messages, placementMessage(placement))
	}
	return messages
}

func placementMessage(placement placementapp.Placement) *placementv1.AgentPlacement {
	state := placementv1.PlacementState_PLACEMENT_STATE_UNSPECIFIED
	switch placement.State {
	case placementdomain.StatePending:
		state = placementv1.PlacementState_PLACEMENT_STATE_PENDING
	case placementdomain.StateReady:
		state = placementv1.PlacementState_PLACEMENT_STATE_READY
	case placementdomain.StateFailed:
		state = placementv1.PlacementState_PLACEMENT_STATE_FAILED
	}
	return &placementv1.AgentPlacement{
		AgentId:              placement.AgentID,
		ComputerId:           placement.ComputerID,
		AgentProfileRevision: placement.AgentProfileRevision,
		AgentProfile:         placementProfileMessage(placement.AgentProfile),
		RuntimeSpec:          placementRuntimeSpecMessage(placement.RuntimeSpec),
		DesiredRevision:      placement.DesiredRevision,
		State:                state,
		ErrorCode:            placement.ErrorCode,
		CreatedAt:            timestamppb.New(placement.CreatedAt),
		UpdatedAt:            timestamppb.New(placement.UpdatedAt),
	}
}

func placementProfileMessage(profile agentapp.Profile) *agentv1.AgentProfile {
	return &agentv1.AgentProfile{
		AgentId: profile.AgentID, Revision: profile.Revision, DisplayName: profile.DisplayName,
		Role: profile.Role, Mission: profile.Mission, Instructions: profile.Instructions,
		CreatedAt: timestamppb.New(profile.CreatedAt),
	}
}

func placementRuntimeSpecMessage(spec agentapp.RuntimeSpec) *agentv1.AgentRuntimeSpec {
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
