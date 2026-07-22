package placement

import (
	placementv1 "github.com/abcdlsj/sumi/gen/go/sumi/placement/v1"
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
	case placementdomain.StateActive:
		state = placementv1.PlacementState_PLACEMENT_STATE_ACTIVE
	case placementdomain.StateFailed:
		state = placementv1.PlacementState_PLACEMENT_STATE_FAILED
	}
	return &placementv1.AgentPlacement{
		AgentId:    placement.AgentID,
		ComputerId: placement.ComputerID,
		Generation: placement.Generation,
		State:      state,
		ErrorCode:  placement.ErrorCode,
		CreatedAt:  timestamppb.New(placement.CreatedAt),
		UpdatedAt:  timestamppb.New(placement.UpdatedAt),
	}
}
