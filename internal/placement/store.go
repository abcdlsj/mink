package placement

import (
	"context"

	"github.com/abcdlsj/sumi/internal/store"
)

type placementStore interface {
	SetAgentPlacement(context.Context, store.SetAgentPlacementParams) (store.AgentPlacement, error)
	GetAgentPlacement(context.Context, string) (store.AgentPlacement, error)
	ListAgentPlacements(context.Context) ([]store.AgentPlacement, error)
	ListComputerAssignments(context.Context, store.ComputerPlacementReadParams) ([]store.AgentPlacement, error)
	ListComputerPlacements(context.Context, store.ComputerPlacementReadParams) ([]store.AgentPlacement, error)
	AcknowledgeAgentPlacement(context.Context, store.AcknowledgePlacementParams) (store.AgentPlacement, error)
}
