package placement

import (
	"context"
	"errors"
	"math"

	"connectrpc.com/connect"
	placementv1 "github.com/abcdlsj/sumi/gen/go/sumi/placement/v1"
	"github.com/abcdlsj/sumi/internal/authority"
	"github.com/abcdlsj/sumi/internal/store"
	"github.com/abcdlsj/sumi/internal/transport/connectid"
)

func (s *Service) SetAgentPlacement(ctx context.Context, request *connect.Request[placementv1.SetAgentPlacementRequest]) (*connect.Response[placementv1.SetAgentPlacementResponse], error) {
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
	computerID, err := connectid.CanonicalID(request.Msg.GetComputerId(), "computer id")
	if err != nil {
		return nil, err
	}
	placement, err := s.store.SetAgentPlacement(ctx, store.SetAgentPlacementParams{
		RequestID: requestID, Actor: actor, AgentID: agentID, ComputerID: computerID, Now: s.now(),
	})
	if err := placementError(err); err != nil {
		return nil, err
	}
	return connect.NewResponse(&placementv1.SetAgentPlacementResponse{Placement: placementMessage(placement)}), nil
}

func (s *Service) GetAgentPlacement(ctx context.Context, request *connect.Request[placementv1.GetAgentPlacementRequest]) (*connect.Response[placementv1.GetAgentPlacementResponse], error) {
	agentID, err := connectid.CanonicalID(request.Msg.GetAgentId(), "agent id")
	if err != nil {
		return nil, err
	}
	placement, err := s.store.GetAgentPlacement(ctx, agentID)
	if err := placementError(err); err != nil {
		return nil, err
	}
	return connect.NewResponse(&placementv1.GetAgentPlacementResponse{Placement: placementMessage(placement)}), nil
}

func (s *Service) ListAgentPlacements(ctx context.Context, _ *connect.Request[placementv1.ListAgentPlacementsRequest]) (*connect.Response[placementv1.ListAgentPlacementsResponse], error) {
	placements, err := s.store.ListAgentPlacements(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&placementv1.ListAgentPlacementsResponse{Placements: placementMessages(placements)}), nil
}

func (s *Service) ListComputerAssignments(ctx context.Context, request *connect.Request[placementv1.ListComputerAssignmentsRequest]) (*connect.Response[placementv1.ListComputerAssignmentsResponse], error) {
	computerID, err := connectid.CanonicalID(request.Msg.GetComputerId(), "computer id")
	if err != nil {
		return nil, err
	}
	if err := registrationKeyValid(request.Msg.GetRegistrationKey()); err != nil {
		return nil, err
	}
	assignments, err := s.store.ListComputerAssignments(ctx, store.ComputerPlacementReadParams{
		ComputerID: computerID, RegistrationKey: request.Msg.GetRegistrationKey(),
	})
	if err := placementError(err); err != nil {
		return nil, err
	}
	return connect.NewResponse(&placementv1.ListComputerAssignmentsResponse{Assignments: placementMessages(assignments)}), nil
}

func (s *Service) ListComputerPlacements(ctx context.Context, request *connect.Request[placementv1.ListComputerPlacementsRequest]) (*connect.Response[placementv1.ListComputerPlacementsResponse], error) {
	computerID, err := connectid.CanonicalID(request.Msg.GetComputerId(), "computer id")
	if err != nil {
		return nil, err
	}
	if err := registrationKeyValid(request.Msg.GetRegistrationKey()); err != nil {
		return nil, err
	}
	placements, err := s.store.ListComputerPlacements(ctx, store.ComputerPlacementReadParams{
		ComputerID: computerID, RegistrationKey: request.Msg.GetRegistrationKey(),
	})
	if err := placementError(err); err != nil {
		return nil, err
	}
	return connect.NewResponse(&placementv1.ListComputerPlacementsResponse{Placements: placementMessages(placements)}), nil
}

func (s *Service) AcknowledgeAgentPlacement(ctx context.Context, request *connect.Request[placementv1.AcknowledgeAgentPlacementRequest]) (*connect.Response[placementv1.AcknowledgeAgentPlacementResponse], error) {
	computerID, err := connectid.CanonicalID(request.Msg.GetComputerId(), "computer id")
	if err != nil {
		return nil, err
	}
	if err := registrationKeyValid(request.Msg.GetRegistrationKey()); err != nil {
		return nil, err
	}
	agentID, err := connectid.CanonicalID(request.Msg.GetAgentId(), "agent id")
	if err != nil {
		return nil, err
	}
	if request.Msg.GetGeneration() == 0 || request.Msg.GetGeneration() > math.MaxInt64 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("generation must be a positive integer"))
	}
	state, errorCode, err := acknowledgement(request.Msg.GetResult(), request.Msg.GetErrorCode())
	if err != nil {
		return nil, err
	}
	placement, err := s.store.AcknowledgeAgentPlacement(ctx, store.AcknowledgePlacementParams{
		ComputerID:      computerID,
		RegistrationKey: request.Msg.GetRegistrationKey(),
		AgentID:         agentID,
		Generation:      request.Msg.GetGeneration(),
		State:           state,
		ErrorCode:       errorCode,
		Now:             s.now(),
	})
	if err := placementError(err); err != nil {
		return nil, err
	}
	return connect.NewResponse(&placementv1.AcknowledgeAgentPlacementResponse{Placement: placementMessage(placement)}), nil
}
