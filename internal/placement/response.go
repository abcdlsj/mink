package placement

import (
	"time"

	agentv1 "github.com/abcdlsj/sumi/gen/go/sumi/agent/v1"
	placementv1 "github.com/abcdlsj/sumi/gen/go/sumi/placement/v1"
	agentapp "github.com/abcdlsj/sumi/internal/agent/application"
	placementapp "github.com/abcdlsj/sumi/internal/placement/application"
	placementdomain "github.com/abcdlsj/sumi/internal/placement/domain"
	"github.com/abcdlsj/sumi/internal/servicesvc"
)

var stateToProto = map[placementdomain.State]placementv1.PlacementState{
	placementdomain.StatePending: placementv1.PlacementState_PLACEMENT_STATE_PENDING,
	placementdomain.StateReady:   placementv1.PlacementState_PLACEMENT_STATE_READY,
	placementdomain.StateFailed:  placementv1.PlacementState_PLACEMENT_STATE_FAILED,
}

func placementsToProto(placements []placementapp.Placement) []*placementv1.AgentPlacement {
	msgs := make([]*placementv1.AgentPlacement, 0, len(placements))
	for _, p := range placements {
		msgs = append(msgs, placementToProto(p))
	}
	return msgs
}

func placementToProto(p placementapp.Placement) *placementv1.AgentPlacement {
	return &placementv1.AgentPlacement{
		AgentId:              p.AgentID,
		ComputerId:           p.ComputerID,
		AgentProfileRevision: p.AgentProfileRevision,
		AgentProfile:         placementProfileToProto(p.AgentProfile),
		RuntimeSpec:          placementSpecToProto(p.RuntimeSpec),
		DesiredRevision:      p.DesiredRevision,
		State:                stateToProto[p.State],
		ErrorCode:            p.ErrorCode,
		CreatedAt:            servicesvc.Ts(p.CreatedAt),
		UpdatedAt:            servicesvc.Ts(p.UpdatedAt),
	}
}

func placementProfileToProto(profile agentapp.Profile) *agentv1.AgentProfile {
	return &agentv1.AgentProfile{
		AgentId: profile.AgentID, Revision: profile.Revision,
		DisplayName: profile.DisplayName, Role: profile.Role,
		Mission: profile.Mission, Instructions: profile.Instructions,
		CreatedAt: servicesvc.Ts(profile.CreatedAt),
	}
}

func placementSpecToProto(spec agentapp.RuntimeSpec) *agentv1.AgentRuntimeSpec {
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
