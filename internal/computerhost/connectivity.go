package computerhost

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"connectrpc.com/connect"
	computerv1 "github.com/abcdlsj/sumi/gen/go/sumi/computer/v1"
	placementv1 "github.com/abcdlsj/sumi/gen/go/sumi/placement/v1"
	runtimev1 "github.com/abcdlsj/sumi/gen/go/sumi/runtime/v1"
	"github.com/abcdlsj/sumi/internal/computerstate"
	"github.com/abcdlsj/sumi/internal/workspace"
	"github.com/google/uuid"
)

func (d *Daemon) connectivitySupervisor(ctx context.Context, identity computerstate.Identity) {
	var loops sync.WaitGroup
	loops.Add(2)
	go func() {
		defer loops.Done()
		d.heartbeatLoop(ctx, identity)
	}()
	go func() {
		defer loops.Done()
		d.snapshotLoop(ctx, identity)
	}()
	loops.Wait()
}

func (d *Daemon) heartbeatLoop(ctx context.Context, identity computerstate.Identity) {
	d.periodicLoop(ctx, d.config.HeartbeatInterval, func(ctx context.Context) error {
		return d.heartbeat(ctx, identity)
	})
}

func (d *Daemon) snapshotLoop(ctx context.Context, identity computerstate.Identity) {
	d.periodicLoop(ctx, d.config.SnapshotInterval, func(ctx context.Context) error {
		return d.syncPlacements(ctx, identity)
	})
}

func (d *Daemon) periodicLoop(ctx context.Context, interval time.Duration, operation func(context.Context) error) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	var failures uint
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			if ctx.Err() != nil {
				return
			}
			err := operation(ctx)
			if ctx.Err() != nil {
				return
			}
			delay := interval
			if err == nil {
				failures = 0
			} else {
				failures++
				delay = retryDelay(failures, interval, d.config.BackoffMax, d.config.RetryJitter)
			}
			timer.Reset(delay)
		}
	}
}

func (d *Daemon) heartbeat(ctx context.Context, identity computerstate.Identity) error {
	sandboxCapability, err := TrustedLocalSandboxCapability()
	if err != nil {
		return fmt.Errorf("build heartbeat sandbox capability: %w", err)
	}
	rpcCtx, cancel := d.rpcContext(ctx)
	defer cancel()
	_, err = d.computers.HeartbeatComputer(rpcCtx, connect.NewRequest(&computerv1.HeartbeatComputerRequest{
		ComputerId: identity.ComputerID, RegistrationKey: identity.RegistrationKey,
		SandboxCapability: sandboxCapability,
	}))
	if err != nil {
		return fmt.Errorf("heartbeat computer: %w", err)
	}
	return nil
}

func (d *Daemon) syncPlacements(ctx context.Context, identity computerstate.Identity) error {
	rpcCtx, cancel := d.rpcContext(ctx)
	response, err := d.placements.ListComputerPlacements(rpcCtx, connect.NewRequest(&placementv1.ListComputerPlacementsRequest{
		ComputerId: identity.ComputerID, RegistrationKey: identity.RegistrationKey,
	}))
	cancel()
	if err != nil {
		return fmt.Errorf("list computer placements: %w", err)
	}
	if response == nil {
		return errors.New("list computer placements returned no response")
	}
	placements := response.Msg.GetPlacements()
	seen := make(map[string]struct{}, len(placements))
	for _, placement := range placements {
		if err := validatePlacement(placement, identity.ComputerID); err != nil {
			return err
		}
		if _, duplicate := seen[placement.GetAgentId()]; duplicate {
			return fmt.Errorf("duplicate placement for agent %q", placement.GetAgentId())
		}
		seen[placement.GetAgentId()] = struct{}{}
	}
	active := make(map[string]uint64)
	for _, placement := range placements {
		if placement.GetState() == placementv1.PlacementState_PLACEMENT_STATE_ACTIVE {
			active[placement.GetAgentId()] = placement.GetGeneration()
		}
	}
	d.reconcileWorkers(active)
	var syncErrors []error
	for _, placement := range placements {
		switch placement.GetState() {
		case placementv1.PlacementState_PLACEMENT_STATE_PENDING:
			placement, err = d.provisionPlacement(ctx, identity, placement)
			if err != nil {
				syncErrors = append(syncErrors, err)
				continue
			}
		case placementv1.PlacementState_PLACEMENT_STATE_ACTIVE:
		case placementv1.PlacementState_PLACEMENT_STATE_FAILED:
		}
		if err := validatePlacement(placement, identity.ComputerID); err != nil {
			syncErrors = append(syncErrors, err)
			continue
		}
		if placement.GetState() == placementv1.PlacementState_PLACEMENT_STATE_ACTIVE {
			active[placement.GetAgentId()] = placement.GetGeneration()
			if err := d.ensureRuntime(ctx, identity, placement.GetAgentId(), placement.GetGeneration()); err != nil {
				syncErrors = append(syncErrors, err)
			}
		}
	}
	sessions, err := d.config.State.RuntimeSessions(ctx)
	if err != nil {
		return fmt.Errorf("list local runtime sessions: %w", err)
	}
	for _, session := range sessions {
		generation, current := active[session.AgentID]
		if current && generation == session.PlacementGeneration && session.ComputerID == identity.ComputerID {
			continue
		}
		if err := d.config.State.DeleteRuntimeSession(ctx, session.AgentID, session.ComputerID, session.PlacementGeneration); err != nil {
			syncErrors = append(syncErrors, fmt.Errorf("delete stale runtime session for agent %q: %w", session.AgentID, err))
		}
		d.stopWorkerBinding(session.AgentID, session.PlacementGeneration)
	}
	return errors.Join(syncErrors...)
}

func validatePlacement(placement *placementv1.AgentPlacement, computerID string) error {
	if placement == nil || placement.GetComputerId() != computerID || placement.GetGeneration() == 0 {
		return errors.New("placement binding is invalid")
	}
	if _, err := uuid.Parse(placement.GetAgentId()); err != nil {
		return fmt.Errorf("placement agent ID is invalid: %w", err)
	}
	switch placement.GetState() {
	case placementv1.PlacementState_PLACEMENT_STATE_PENDING,
		placementv1.PlacementState_PLACEMENT_STATE_ACTIVE,
		placementv1.PlacementState_PLACEMENT_STATE_FAILED:
		return nil
	default:
		return errors.New("placement state is invalid")
	}
}

func (d *Daemon) provisionPlacement(ctx context.Context, identity computerstate.Identity, placement *placementv1.AgentPlacement) (*placementv1.AgentPlacement, error) {
	_, provisionErr := workspace.Provision(d.config.DataRoot, placement.GetAgentId())
	result := placementv1.AcknowledgementResult_ACKNOWLEDGEMENT_RESULT_ACTIVE
	errorCode := ""
	if provisionErr != nil {
		result = placementv1.AcknowledgementResult_ACKNOWLEDGEMENT_RESULT_FAILED
		errorCode = workspace.ErrorCode(provisionErr)
	}
	rpcCtx, cancel := d.rpcContext(ctx)
	defer cancel()
	response, err := d.placements.AcknowledgeAgentPlacement(rpcCtx, connect.NewRequest(&placementv1.AcknowledgeAgentPlacementRequest{
		ComputerId: identity.ComputerID, RegistrationKey: identity.RegistrationKey,
		AgentId: placement.GetAgentId(), Generation: placement.GetGeneration(), Result: result, ErrorCode: errorCode,
	}))
	if err != nil {
		return nil, fmt.Errorf("acknowledge placement for agent %q: %w", placement.GetAgentId(), err)
	}
	if response == nil {
		return nil, errors.New("acknowledge placement returned no response")
	}
	return response.Msg.GetPlacement(), nil
}

func (d *Daemon) ensureRuntime(ctx context.Context, identity computerstate.Identity, agentID string, generation uint64) error {
	now := d.config.Now()
	session, found, err := d.config.State.RuntimeSession(ctx, agentID)
	if err != nil {
		return fmt.Errorf("read runtime session for agent %q: %w", agentID, err)
	}
	if found && session.ComputerID == identity.ComputerID && session.PlacementGeneration == generation && session.ExpiresAt.After(now.Add(d.config.RuntimeRenewBefore)) {
		return nil
	}
	if found && session.ComputerID == identity.ComputerID && session.PlacementGeneration == generation {
		rpcCtx, cancel := d.rpcContext(ctx)
		request := runtimeRequest(session.Token, &runtimev1.RenewAgentRuntimeSessionRequest{
			ComputerId: identity.ComputerID, RegistrationKey: identity.RegistrationKey,
		})
		response, renewErr := d.runtimes.RenewAgentRuntimeSession(rpcCtx, request)
		cancel()
		if renewErr == nil && response != nil {
			return d.saveRuntimeResponse(ctx, response.Msg.GetSession(), agentID, identity.ComputerID, generation, now)
		}
		if renewErr == nil {
			return errors.New("renew runtime session returned no response")
		}
		if connect.CodeOf(renewErr) != connect.CodeUnauthenticated {
			return fmt.Errorf("renew runtime session for agent %q: %w", agentID, renewErr)
		}
		if err := d.config.State.DeleteRuntimeSession(ctx, agentID, identity.ComputerID, generation); err != nil {
			return fmt.Errorf("delete rejected runtime session for agent %q: %w", agentID, err)
		}
	}
	rpcCtx, cancel := d.rpcContext(ctx)
	response, err := d.runtimes.CreateAgentRuntimeSession(rpcCtx, connect.NewRequest(&runtimev1.CreateAgentRuntimeSessionRequest{
		ComputerId: identity.ComputerID, RegistrationKey: identity.RegistrationKey,
		AgentId: agentID, PlacementGeneration: generation,
	}))
	cancel()
	if err != nil {
		return fmt.Errorf("create runtime session for agent %q: %w", agentID, err)
	}
	if response == nil {
		return errors.New("create runtime session returned no response")
	}
	return d.saveRuntimeResponse(ctx, response.Msg.GetSession(), agentID, identity.ComputerID, generation, now)
}

func (d *Daemon) saveRuntimeResponse(ctx context.Context, session *runtimev1.AgentRuntimeSession, agentID, computerID string, generation uint64, updatedAt time.Time) error {
	if session == nil || session.GetAgentId() != agentID || session.GetComputerId() != computerID ||
		session.GetPlacementGeneration() != generation || session.GetExpiresAt() == nil ||
		session.GetExpiresAt().CheckValid() != nil || !session.GetExpiresAt().AsTime().After(updatedAt) ||
		!canonicalSecret(session.GetToken()) {
		return errors.New("runtime session response is invalid")
	}
	if err := d.config.State.SaveRuntimeSession(ctx, computerstate.RuntimeSession{
		AgentID: session.GetAgentId(), ComputerID: session.GetComputerId(),
		PlacementGeneration: session.GetPlacementGeneration(), Token: session.GetToken(),
		ExpiresAt: session.GetExpiresAt().AsTime(), UpdatedAt: updatedAt,
	}); err != nil {
		return fmt.Errorf("save runtime session for agent %q: %w", agentID, err)
	}
	return nil
}
