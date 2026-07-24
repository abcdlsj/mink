package placement

import (
	"context"
	"math"

	"connectrpc.com/connect"
	placementv1 "github.com/abcdlsj/sumi/gen/go/sumi/placement/v1"
	"github.com/abcdlsj/sumi/internal/authority"
	placementapp "github.com/abcdlsj/sumi/internal/placement/application"
	"github.com/abcdlsj/sumi/internal/servicesvc"
	"github.com/abcdlsj/sumi/internal/transport/connectid"
)

func (s *Service) SetAgentPlacement(ctx context.Context, req *connect.Request[placementv1.SetAgentPlacementRequest]) (*connect.Response[placementv1.SetAgentPlacementResponse], error) {
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
	computerID, err := connectid.CanonicalID(req.Msg.GetComputerId(), "computer id")
	if err != nil {
		return nil, err
	}
	placement, err := s.store.SetAgentPlacement(ctx, placementapp.SetCommand{
		RequestID: requestID, Actor: actor,
		AgentID: agentID, ComputerID: computerID, Now: s.now(),
	})
	if err := placementErr(err); err != nil {
		return nil, err
	}
	return connect.NewResponse(&placementv1.SetAgentPlacementResponse{Placement: placementToProto(placement)}), nil
}

func (s *Service) GetAgentPlacement(ctx context.Context, req *connect.Request[placementv1.GetAgentPlacementRequest]) (*connect.Response[placementv1.GetAgentPlacementResponse], error) {
	agentID, err := connectid.CanonicalID(req.Msg.GetAgentId(), "agent id")
	if err != nil {
		return nil, err
	}
	placement, err := s.store.GetAgentPlacement(ctx, agentID)
	if err := placementErr(err); err != nil {
		return nil, err
	}
	return connect.NewResponse(&placementv1.GetAgentPlacementResponse{Placement: placementToProto(placement)}), nil
}

func (s *Service) ListAgentPlacements(ctx context.Context, _ *connect.Request[placementv1.ListAgentPlacementsRequest]) (*connect.Response[placementv1.ListAgentPlacementsResponse], error) {
	placements, err := s.store.ListAgentPlacements(ctx)
	if err != nil {
		return nil, servicesvc.ErrInternal
	}
	return connect.NewResponse(&placementv1.ListAgentPlacementsResponse{
		Placements: placementsToProto(placements),
	}), nil
}

func (s *Service) ListComputerAssignments(ctx context.Context, req *connect.Request[placementv1.ListComputerAssignmentsRequest]) (*connect.Response[placementv1.ListComputerAssignmentsResponse], error) {
	computerID, err := connectid.CanonicalID(req.Msg.GetComputerId(), "computer id")
	if err != nil {
		return nil, err
	}
	if err := regKeyValid(req.Msg.GetRegistrationKey()); err != nil {
		return nil, err
	}
	assignments, err := s.store.ListComputerAssignments(ctx, placementapp.ComputerReadQuery{
		ComputerID: computerID, RegistrationKey: req.Msg.GetRegistrationKey(),
	})
	if err := placementErr(err); err != nil {
		return nil, err
	}
	return connect.NewResponse(&placementv1.ListComputerAssignmentsResponse{
		Assignments: placementsToProto(assignments),
	}), nil
}

func (s *Service) ListComputerPlacements(ctx context.Context, req *connect.Request[placementv1.ListComputerPlacementsRequest]) (*connect.Response[placementv1.ListComputerPlacementsResponse], error) {
	computerID, err := connectid.CanonicalID(req.Msg.GetComputerId(), "computer id")
	if err != nil {
		return nil, err
	}
	if err := regKeyValid(req.Msg.GetRegistrationKey()); err != nil {
		return nil, err
	}
	placements, err := s.store.ListComputerPlacements(ctx, placementapp.ComputerReadQuery{
		ComputerID: computerID, RegistrationKey: req.Msg.GetRegistrationKey(),
	})
	if err := placementErr(err); err != nil {
		return nil, err
	}
	return connect.NewResponse(&placementv1.ListComputerPlacementsResponse{
		Placements: placementsToProto(placements),
	}), nil
}

func (s *Service) AcknowledgeAgentPlacement(ctx context.Context, req *connect.Request[placementv1.AcknowledgeAgentPlacementRequest]) (*connect.Response[placementv1.AcknowledgeAgentPlacementResponse], error) {
	computerID, err := connectid.CanonicalID(req.Msg.GetComputerId(), "computer id")
	if err != nil {
		return nil, err
	}
	if err := regKeyValid(req.Msg.GetRegistrationKey()); err != nil {
		return nil, err
	}
	agentID, err := connectid.CanonicalID(req.Msg.GetAgentId(), "agent id")
	if err != nil {
		return nil, err
	}
	rev := req.Msg.GetDesiredRevision()
	if rev == 0 || rev > math.MaxInt64 {
		return nil, servicesvc.InvalArg("desired revision must be a positive integer")
	}
	state, errCode, err := parseAckResult(req.Msg.GetResult(), req.Msg.GetErrorCode())
	if err != nil {
		return nil, err
	}
	placement, err := s.store.AcknowledgeAgentPlacement(ctx, placementapp.AcknowledgeCommand{
		ComputerID: computerID, RegistrationKey: req.Msg.GetRegistrationKey(),
		AgentID: agentID, DesiredRevision: rev,
		State: state, ErrorCode: errCode, Now: s.now(),
	})
	if err := placementErr(err); err != nil {
		return nil, err
	}
	return connect.NewResponse(&placementv1.AcknowledgeAgentPlacementResponse{Placement: placementToProto(placement)}), nil
}
