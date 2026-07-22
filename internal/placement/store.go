package placement

import (
	"context"

	placementapp "github.com/abcdlsj/sumi/internal/placement/application"
)

type placementStore interface {
	SetAgentPlacement(context.Context, placementapp.SetCommand) (placementapp.Placement, error)
	GetAgentPlacement(context.Context, string) (placementapp.Placement, error)
	ListAgentPlacements(context.Context) ([]placementapp.Placement, error)
	ListComputerAssignments(context.Context, placementapp.ComputerReadQuery) ([]placementapp.Placement, error)
	ListComputerPlacements(context.Context, placementapp.ComputerReadQuery) ([]placementapp.Placement, error)
	AcknowledgeAgentPlacement(context.Context, placementapp.AcknowledgeCommand) (placementapp.Placement, error)
}
