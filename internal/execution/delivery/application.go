package delivery

import (
	"context"

	executionapp "github.com/abcdlsj/sumi/internal/execution/application"
	execution "github.com/abcdlsj/sumi/internal/execution/domain"
)

type ListDeliveriesCommand = executionapp.ListDeliveriesQuery

type AcceptDeliveryCommand = executionapp.AcceptDeliveryCommand

type GetRunCommand = executionapp.GetRunQuery

type ClaimRunCommand = executionapp.ClaimRunCommand

type RenewRunCommand = executionapp.RenewRunCommand

type CompleteRunCommand = executionapp.CompleteRunCommand

func (s *Service) listDeliveries(ctx context.Context, command ListDeliveriesCommand) (DeliveryListResult, error) {
	result, err := s.store.ListDeliveries(ctx, command)
	if err != nil {
		return DeliveryListResult{}, err
	}
	return DeliveryListResult{
		Deliveries: mapDeliveries(result.Deliveries), NextSequence: result.NextSequence,
		ActiveDelivery: mapDelivery(result.ActiveDelivery), ActiveRun: mapRun(result.ActiveRun), ActiveLaunch: mapLaunch(result.ActiveLaunch),
	}, nil
}

func (s *Service) acceptDelivery(ctx context.Context, command AcceptDeliveryCommand) (execution.Run, error) {
	run, err := s.store.AcceptDelivery(ctx, command)
	if err != nil {
		return execution.Run{}, err
	}
	return executionRun(run), nil
}

func (s *Service) getRun(ctx context.Context, command GetRunCommand) (execution.Run, error) {
	run, err := s.store.GetRun(ctx, command)
	if err != nil {
		return execution.Run{}, err
	}
	return executionRun(run), nil
}

func (s *Service) claimRun(ctx context.Context, command ClaimRunCommand) (execution.Launch, error) {
	launch, err := s.store.ClaimRun(ctx, command)
	if err != nil {
		return execution.Launch{}, err
	}
	return executionLaunch(launch), nil
}

func (s *Service) renewRun(ctx context.Context, command RenewRunCommand) (execution.Launch, error) {
	launch, err := s.store.RenewRun(ctx, command)
	if err != nil {
		return execution.Launch{}, err
	}
	return executionLaunch(launch), nil
}

func (s *Service) completeRun(ctx context.Context, command CompleteRunCommand) (CompleteRunResult, error) {
	result, err := s.store.CompleteRun(ctx, command)
	if err != nil {
		return CompleteRunResult{}, err
	}
	return mapCompleteRun(result), nil
}
