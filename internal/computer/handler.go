package computer

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	computerv1 "github.com/abcdlsj/sumi/gen/go/sumi/computer/v1"
	"github.com/abcdlsj/sumi/internal/authority"
	computerapp "github.com/abcdlsj/sumi/internal/computer/application"
	"github.com/abcdlsj/sumi/internal/transport/connectid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (s *Service) CreateComputerPairing(ctx context.Context, request *connect.Request[computerv1.CreateComputerPairingRequest]) (*connect.Response[computerv1.CreateComputerPairingResponse], error) {
	actor, err := authority.Subject(ctx)
	if err != nil {
		return nil, err
	}
	requestID, err := connectid.CanonicalID(request.Msg.GetRequestId(), "request id")
	if err != nil {
		return nil, err
	}
	if err := pairingTokenValid(request.Msg.GetPairingToken()); err != nil {
		return nil, err
	}
	expiresAt := request.Msg.GetExpiresAt()
	if expiresAt == nil || expiresAt.CheckValid() != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("pairing expiry is invalid"))
	}
	pairing, err := s.store.CreateComputerPairing(ctx, computerapp.PreparePairingCommand{
		RequestID: requestID,
		Actor:     actor,
		Token:     request.Msg.GetPairingToken(),
		ExpiresAt: expiresAt.AsTime(),
		Now:       s.now(),
	})
	if err := pairingError(err); err != nil {
		return nil, err
	}
	return connect.NewResponse(&computerv1.CreateComputerPairingResponse{
		PairingId: pairing.ID,
		ExpiresAt: timestamppb.New(pairing.ExpiresAt),
	}), nil
}

func (s *Service) RegisterComputer(ctx context.Context, request *connect.Request[computerv1.RegisterComputerRequest]) (*connect.Response[computerv1.RegisterComputerResponse], error) {
	requestID, err := connectid.CanonicalID(request.Msg.GetRequestId(), "request id")
	if err != nil {
		return nil, err
	}
	request.Msg.RequestId = requestID
	params, err := pairParams(request.Msg, s.now())
	if err != nil {
		return nil, err
	}
	computer, err := s.store.PairComputer(ctx, params)
	if err := pairingError(err); err != nil {
		return nil, err
	}
	return connect.NewResponse(&computerv1.RegisterComputerResponse{Computer: computerMessage(computer, s.now())}), nil
}

func (s *Service) HeartbeatComputer(ctx context.Context, request *connect.Request[computerv1.HeartbeatComputerRequest]) (*connect.Response[computerv1.HeartbeatComputerResponse], error) {
	id, err := connectid.CanonicalID(request.Msg.GetComputerId(), "computer id")
	if err != nil {
		return nil, err
	}
	if err := registrationKeyValid(request.Msg.GetRegistrationKey()); err != nil {
		return nil, err
	}
	inventory, err := capabilityInventory(request.Msg.GetCapabilityInventory())
	if err != nil {
		return nil, err
	}
	computer, err := s.store.HeartbeatComputer(ctx, computerapp.HeartbeatCommand{
		ComputerID: id, RegistrationKey: request.Msg.GetRegistrationKey(),
		CapabilityInventory: inventory, Now: s.now(),
	})
	if errors.Is(err, computerapp.ErrNotFound) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("computer not found"))
	}
	if errors.Is(err, computerapp.ErrRegistrationKeyMismatch) {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("computer credentials do not match"))
	}
	if errors.Is(err, computerapp.ErrCapabilityInventoryInvalid) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("capability inventory is invalid"))
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&computerv1.HeartbeatComputerResponse{Computer: computerMessage(computer, s.now())}), nil
}

func (s *Service) GetComputer(ctx context.Context, request *connect.Request[computerv1.GetComputerRequest]) (*connect.Response[computerv1.GetComputerResponse], error) {
	id, err := connectid.CanonicalID(request.Msg.GetComputerId(), "computer id")
	if err != nil {
		return nil, err
	}
	computer, err := s.store.GetComputer(ctx, id)
	if errors.Is(err, computerapp.ErrNotFound) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("computer not found"))
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&computerv1.GetComputerResponse{Computer: computerMessage(computer, s.now())}), nil
}

func (s *Service) ListComputers(ctx context.Context, _ *connect.Request[computerv1.ListComputersRequest]) (*connect.Response[computerv1.ListComputersResponse], error) {
	computers, err := s.store.ListComputers(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	response := &computerv1.ListComputersResponse{Computers: make([]*computerv1.Computer, 0, len(computers))}
	now := s.now()
	for _, computer := range computers {
		response.Computers = append(response.Computers, computerMessage(computer, now))
	}
	return connect.NewResponse(response), nil
}

func (s *Service) EnqueueCredentialDelivery(ctx context.Context, request *connect.Request[computerv1.EnqueueCredentialDeliveryRequest]) (*connect.Response[computerv1.EnqueueCredentialDeliveryResponse], error) {
	actor, err := authority.Subject(ctx)
	if err != nil {
		return nil, err
	}
	requestID, err := connectid.CanonicalID(request.Msg.GetRequestId(), "request id")
	if err != nil {
		return nil, err
	}
	computerID, err := connectid.CanonicalID(request.Msg.GetComputerId(), "computer id")
	if err != nil {
		return nil, err
	}
	agentID, err := connectid.CanonicalID(request.Msg.GetAgentId(), "agent id")
	if err != nil {
		return nil, err
	}
	credentialKind, ok := credentialKindName(request.Msg.GetCredentialKind())
	if !ok {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("credential kind is invalid"))
	}
	sealed, err := sealedCredential(request.Msg.GetSealedCredential())
	if err != nil {
		return nil, err
	}
	expiresAt := request.Msg.GetExpiresAt()
	if expiresAt == nil || expiresAt.CheckValid() != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("credential delivery expiry is invalid"))
	}
	delivery, err := s.store.EnqueueCredentialDelivery(ctx, computerapp.EnqueueCredentialDeliveryCommand{
		RequestID: requestID, Actor: actor, ComputerID: computerID, AgentID: agentID,
		CredentialKind: credentialKind, Sealed: sealed, ExpiresAt: expiresAt.AsTime(), Now: s.now(),
	})
	if mapped := credentialDeliveryError(err); mapped != nil {
		return nil, mapped
	}
	return connect.NewResponse(&computerv1.EnqueueCredentialDeliveryResponse{Delivery: credentialDeliveryMessage(delivery)}), nil
}

func (s *Service) ListCredentialDeliveries(ctx context.Context, request *connect.Request[computerv1.ListCredentialDeliveriesRequest]) (*connect.Response[computerv1.ListCredentialDeliveriesResponse], error) {
	actor, err := authority.Subject(ctx)
	if err != nil {
		return nil, err
	}
	agentID, err := connectid.CanonicalID(request.Msg.GetAgentId(), "agent id")
	if err != nil {
		return nil, err
	}
	computerID := ""
	if request.Msg.GetComputerId() != "" {
		computerID, err = connectid.CanonicalID(request.Msg.GetComputerId(), "computer id")
		if err != nil {
			return nil, err
		}
	}
	deliveries, err := s.store.ListCredentialDeliveries(ctx, computerapp.ListCredentialDeliveriesQuery{
		Actor: actor, ComputerID: computerID, AgentID: agentID, Now: s.now(),
	})
	if mapped := credentialDeliveryError(err); mapped != nil {
		return nil, mapped
	}
	response := &computerv1.ListCredentialDeliveriesResponse{Deliveries: make([]*computerv1.CredentialDelivery, 0, len(deliveries))}
	for _, delivery := range deliveries {
		response.Deliveries = append(response.Deliveries, credentialDeliveryMessage(delivery))
	}
	return connect.NewResponse(response), nil
}

func (s *Service) ClaimCredentialDelivery(ctx context.Context, request *connect.Request[computerv1.ClaimCredentialDeliveryRequest]) (*connect.Response[computerv1.ClaimCredentialDeliveryResponse], error) {
	computerID, err := connectid.CanonicalID(request.Msg.GetComputerId(), "computer id")
	if err != nil {
		return nil, err
	}
	if err := registrationKeyValid(request.Msg.GetRegistrationKey()); err != nil {
		return nil, err
	}
	delivery, err := s.store.ClaimCredentialDelivery(ctx, computerapp.ClaimCredentialDeliveryCommand{
		ComputerID: computerID, RegistrationKey: request.Msg.GetRegistrationKey(), Now: s.now(),
	})
	if errors.Is(err, computerapp.ErrNotFound) {
		return connect.NewResponse(&computerv1.ClaimCredentialDeliveryResponse{}), nil
	}
	if mapped := credentialDeliveryError(err); mapped != nil {
		return nil, mapped
	}
	return connect.NewResponse(&computerv1.ClaimCredentialDeliveryResponse{Delivery: credentialDeliveryMessage(delivery)}), nil
}

func (s *Service) CompleteCredentialDelivery(ctx context.Context, request *connect.Request[computerv1.CompleteCredentialDeliveryRequest]) (*connect.Response[computerv1.CompleteCredentialDeliveryResponse], error) {
	computerID, err := connectid.CanonicalID(request.Msg.GetComputerId(), "computer id")
	if err != nil {
		return nil, err
	}
	deliveryID, err := connectid.CanonicalID(request.Msg.GetDeliveryId(), "delivery id")
	if err != nil {
		return nil, err
	}
	if err := registrationKeyValid(request.Msg.GetRegistrationKey()); err != nil {
		return nil, err
	}
	if !credentialCompletionValid(request.Msg.GetBindingHandle(), request.Msg.GetErrorCode()) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("credential delivery completion is invalid"))
	}
	delivery, err := s.store.CompleteCredentialDelivery(ctx, computerapp.CompleteCredentialDeliveryCommand{
		ComputerID: computerID, RegistrationKey: request.Msg.GetRegistrationKey(), DeliveryID: deliveryID,
		BindingHandle: request.Msg.GetBindingHandle(), ErrorCode: request.Msg.GetErrorCode(), Now: s.now(),
	})
	if mapped := credentialDeliveryError(err); mapped != nil {
		return nil, mapped
	}
	return connect.NewResponse(&computerv1.CompleteCredentialDeliveryResponse{Delivery: credentialDeliveryMessage(delivery)}), nil
}

func credentialDeliveryError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, computerapp.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, errors.New("credential delivery not found"))
	case errors.Is(err, computerapp.ErrRegistrationKeyMismatch), errors.Is(err, computerapp.ErrCredentialDeliveryDenied):
		return connect.NewError(connect.CodePermissionDenied, errors.New("credential delivery is not authorized"))
	case errors.Is(err, computerapp.ErrCredentialDeliveryInvalid):
		return connect.NewError(connect.CodeInvalidArgument, errors.New("credential delivery is invalid"))
	case errors.Is(err, computerapp.ErrCredentialDeliveryConflict):
		return connect.NewError(connect.CodeAlreadyExists, errors.New("credential delivery conflicts with current facts"))
	default:
		return connect.NewError(connect.CodeInternal, errors.New("credential delivery failed"))
	}
}
