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
	params, err := registerParams(request.Msg, s.now())
	if err != nil {
		return nil, err
	}
	var computer computerapp.Computer
	if request.Msg.GetPairingToken() == "" {
		computer, err = s.store.RecoverComputer(ctx, params)
	} else {
		requestID, idErr := connectid.CanonicalID(request.Msg.GetRequestId(), "request id")
		if idErr != nil {
			return nil, idErr
		}
		if err := pairingTokenValid(request.Msg.GetPairingToken()); err != nil {
			return nil, err
		}
		computer, err = s.store.PairComputer(ctx, computerapp.PairCommand{
			RequestID:         requestID,
			PairingToken:      request.Msg.GetPairingToken(),
			RegistrationKey:   params.RegistrationKey,
			Name:              params.Name,
			OS:                params.OS,
			Arch:              params.Arch,
			SandboxCapability: params.SandboxCapability,
			Now:               params.Now,
		})
	}
	if errors.Is(err, computerapp.ErrNotFound) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("computer not found"))
	}
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
	capability, err := sandboxCapability(request.Msg.GetSandboxCapability())
	if err != nil {
		return nil, err
	}
	computer, err := s.store.HeartbeatComputer(ctx, computerapp.HeartbeatCommand{
		ComputerID: id, RegistrationKey: request.Msg.GetRegistrationKey(),
		SandboxCapability: capability, Now: s.now(),
	})
	if errors.Is(err, computerapp.ErrNotFound) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("computer not found"))
	}
	if errors.Is(err, computerapp.ErrRegistrationKeyMismatch) {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("computer credentials do not match"))
	}
	if errors.Is(err, computerapp.ErrSandboxCapabilityInvalid) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("sandbox capability is invalid"))
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
