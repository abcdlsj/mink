package placement

import (
	placementv1 "github.com/abcdlsj/sumi/gen/go/sumi/placement/v1"
	"github.com/abcdlsj/sumi/internal/store"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func placementMessages(placements []store.AgentPlacement) []*placementv1.AgentPlacement {
	messages := make([]*placementv1.AgentPlacement, 0, len(placements))
	for _, placement := range placements {
		messages = append(messages, placementMessage(placement))
	}
	return messages
}

func placementMessage(placement store.AgentPlacement) *placementv1.AgentPlacement {
	state := placementv1.PlacementState_PLACEMENT_STATE_UNSPECIFIED
	switch placement.State {
	case "pending":
		state = placementv1.PlacementState_PLACEMENT_STATE_PENDING
	case "active":
		state = placementv1.PlacementState_PLACEMENT_STATE_ACTIVE
	case "failed":
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
