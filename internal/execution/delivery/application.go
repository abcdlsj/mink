package delivery

import (
	"context"
	"time"

	execution "github.com/abcdlsj/sumi/internal/execution/domain"
	"github.com/abcdlsj/sumi/internal/store"
)

type ListDeliveriesCommand struct {
	Authentication store.AgentRuntimeAuthentication
	AfterSequence  uint64
	Limit          uint32
	Now            time.Time
}

type AcceptDeliveryCommand struct {
	RequestID      string
	Authentication store.AgentRuntimeAuthentication
	DeliveryID     string
	Now            time.Time
}

type GetRunCommand struct {
	Authentication store.AgentRuntimeAuthentication
	RunID          string
	Now            time.Time
}

type ClaimRunCommand struct {
	RequestID      string
	Authentication store.AgentRuntimeAuthentication
	RunID          string
	Now            time.Time
}

type RenewRunCommand struct {
	RequestID      string
	Authentication store.AgentRuntimeAuthentication
	RunID          string
	LaunchID       string
	Fence          uint64
	Now            time.Time
}

type CompleteRunCommand struct {
	RequestID         string
	OutboxEventID     string
	Authentication    store.AgentRuntimeAuthentication
	RunID             string
	LaunchID          string
	Fence             uint64
	Outcome           execution.Outcome
	Body              string
	MentionedAgentIDs []string
	Now               time.Time
}

func (s *Service) listDeliveries(ctx context.Context, command ListDeliveriesCommand) (DeliveryListResult, error) {
	result, err := s.store.ListDeliveries(ctx, store.ListDeliveriesParams{
		Authentication: command.Authentication, AfterSequence: command.AfterSequence, Limit: command.Limit, Now: command.Now,
	})
	if err != nil {
		return DeliveryListResult{}, err
	}
	return DeliveryListResult{
		Deliveries: mapDeliveries(result.Deliveries), NextSequence: result.NextSequence,
		ActiveDelivery: mapDelivery(result.ActiveDelivery), ActiveRun: mapRun(result.ActiveRun), ActiveLaunch: mapLaunch(result.ActiveLaunch),
	}, nil
}

func (s *Service) acceptDelivery(ctx context.Context, command AcceptDeliveryCommand) (execution.Run, error) {
	run, err := s.store.AcceptDelivery(ctx, store.AcceptDeliveryParams{
		RequestID: command.RequestID, Authentication: command.Authentication, DeliveryID: command.DeliveryID, Now: command.Now,
	})
	if err != nil {
		return execution.Run{}, err
	}
	return executionRun(run), nil
}

func (s *Service) getRun(ctx context.Context, command GetRunCommand) (execution.Run, error) {
	run, err := s.store.GetRun(ctx, store.GetRunParams{
		Authentication: command.Authentication, RunID: command.RunID, Now: command.Now,
	})
	if err != nil {
		return execution.Run{}, err
	}
	return executionRun(run), nil
}

func (s *Service) claimRun(ctx context.Context, command ClaimRunCommand) (execution.Launch, error) {
	launch, err := s.store.ClaimRun(ctx, store.ClaimRunParams{
		RequestID: command.RequestID, Authentication: command.Authentication, RunID: command.RunID, Now: command.Now,
	})
	if err != nil {
		return execution.Launch{}, err
	}
	return executionLaunch(launch), nil
}

func (s *Service) renewRun(ctx context.Context, command RenewRunCommand) (execution.Launch, error) {
	launch, err := s.store.RenewRun(ctx, store.RenewRunParams{
		RequestID: command.RequestID, Authentication: command.Authentication, RunID: command.RunID,
		LaunchID: command.LaunchID, Fence: command.Fence, Now: command.Now,
	})
	if err != nil {
		return execution.Launch{}, err
	}
	return executionLaunch(launch), nil
}

func (s *Service) completeRun(ctx context.Context, command CompleteRunCommand) (CompleteRunResult, error) {
	result, err := s.store.CompleteRun(ctx, store.CompleteRunParams{
		RequestID: command.RequestID, OutboxEventID: command.OutboxEventID, Authentication: command.Authentication,
		RunID: command.RunID, LaunchID: command.LaunchID, Fence: command.Fence, Outcome: string(command.Outcome),
		Body: command.Body, MentionedAgentIDs: command.MentionedAgentIDs, Now: command.Now,
	})
	if err != nil {
		return CompleteRunResult{}, err
	}
	return mapCompleteRun(result), nil
}
