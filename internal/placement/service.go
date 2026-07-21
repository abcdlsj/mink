package placement

import (
	"context"
	"errors"
	"math"
	"time"

	"connectrpc.com/connect"
	placementv1 "github.com/abcdlsj/sumi/gen/go/sumi/placement/v1"
	"github.com/abcdlsj/sumi/internal/authority"
	"github.com/abcdlsj/sumi/internal/connectapi"
	"github.com/abcdlsj/sumi/internal/placementcode"
	"github.com/abcdlsj/sumi/internal/store"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Service struct {
	store *store.Store
	now   func() time.Time
}

func New(database *store.Store) *Service {
	return &Service{store: database, now: time.Now}
}

func (s *Service) SetAgentPlacement(ctx context.Context, request *connect.Request[placementv1.SetAgentPlacementRequest]) (*connect.Response[placementv1.SetAgentPlacementResponse], error) {
	actor, err := authority.Subject(ctx)
	if err != nil {
		return nil, err
	}
	requestID, err := connectapi.CanonicalID(request.Msg.GetRequestId(), "request id")
	if err != nil {
		return nil, err
	}
	agentID, err := connectapi.CanonicalID(request.Msg.GetAgentId(), "agent id")
	if err != nil {
		return nil, err
	}
	computerID, err := connectapi.CanonicalID(request.Msg.GetComputerId(), "computer id")
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
	agentID, err := connectapi.CanonicalID(request.Msg.GetAgentId(), "agent id")
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
	computerID, err := connectapi.CanonicalID(request.Msg.GetComputerId(), "computer id")
	if err != nil {
		return nil, err
	}
	if err := registrationKeyValid(request.Msg.GetRegistrationKey()); err != nil {
		return nil, err
	}
	assignments, err := s.store.ListComputerAssignments(ctx, computerID, request.Msg.GetRegistrationKey())
	if err := placementError(err); err != nil {
		return nil, err
	}
	return connect.NewResponse(&placementv1.ListComputerAssignmentsResponse{Assignments: placementMessages(assignments)}), nil
}

func (s *Service) ListComputerPlacements(ctx context.Context, request *connect.Request[placementv1.ListComputerPlacementsRequest]) (*connect.Response[placementv1.ListComputerPlacementsResponse], error) {
	computerID, err := connectapi.CanonicalID(request.Msg.GetComputerId(), "computer id")
	if err != nil {
		return nil, err
	}
	if err := registrationKeyValid(request.Msg.GetRegistrationKey()); err != nil {
		return nil, err
	}
	placements, err := s.store.ListComputerPlacements(ctx, computerID, request.Msg.GetRegistrationKey())
	if err := placementError(err); err != nil {
		return nil, err
	}
	return connect.NewResponse(&placementv1.ListComputerPlacementsResponse{Placements: placementMessages(placements)}), nil
}

func (s *Service) AcknowledgeAgentPlacement(ctx context.Context, request *connect.Request[placementv1.AcknowledgeAgentPlacementRequest]) (*connect.Response[placementv1.AcknowledgeAgentPlacementResponse], error) {
	computerID, err := connectapi.CanonicalID(request.Msg.GetComputerId(), "computer id")
	if err != nil {
		return nil, err
	}
	if err := registrationKeyValid(request.Msg.GetRegistrationKey()); err != nil {
		return nil, err
	}
	agentID, err := connectapi.CanonicalID(request.Msg.GetAgentId(), "agent id")
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

func registrationKeyValid(key string) error {
	if key == "" {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("registration key is required"))
	}
	if len(key) > 256 {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("registration key is too long"))
	}
	return nil
}

func acknowledgement(result placementv1.AcknowledgementResult, errorCode string) (string, string, error) {
	switch result {
	case placementv1.AcknowledgementResult_ACKNOWLEDGEMENT_RESULT_ACTIVE:
		if errorCode != "" {
			return "", "", connect.NewError(connect.CodeInvalidArgument, errors.New("active acknowledgement cannot include an error code"))
		}
		return "active", "", nil
	case placementv1.AcknowledgementResult_ACKNOWLEDGEMENT_RESULT_FAILED:
		if !placementcode.Valid(errorCode) {
			return "", "", connect.NewError(connect.CodeInvalidArgument, errors.New("failed acknowledgement requires a known error code"))
		}
		return "failed", errorCode, nil
	default:
		return "", "", connect.NewError(connect.CodeInvalidArgument, errors.New("acknowledgement result must be active or failed"))
	}
}

func placementError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrAgentNotFound), errors.Is(err, store.ErrComputerNotFound), errors.Is(err, store.ErrPlacementNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, store.ErrRegistrationKeyMismatch):
		return connect.NewError(connect.CodePermissionDenied, errors.New("computer credentials do not match"))
	case errors.Is(err, store.ErrPermissionDenied):
		return connect.NewError(connect.CodePermissionDenied, errors.New("agent placement denied"))
	case errors.Is(err, store.ErrPlacementRequestConflict):
		return connect.NewError(connect.CodeAlreadyExists, errors.New("request id already exists with different placement data"))
	case errors.Is(err, store.ErrPlacementStale), errors.Is(err, store.ErrPlacementConflict):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	default:
		return connect.NewError(connect.CodeInternal, err)
	}
}

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
