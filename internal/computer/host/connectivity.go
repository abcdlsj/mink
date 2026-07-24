package host

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
	computerruntime "github.com/abcdlsj/sumi/internal/computer/runtime"
	computerstate "github.com/abcdlsj/sumi/internal/computer/state"
	"github.com/abcdlsj/sumi/internal/observability"
	"github.com/abcdlsj/sumi/internal/workspace"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
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
	d.periodicLoop(ctx, d.config.HeartbeatInterval, d.transportLogger, "computer.heartbeat", func(ctx context.Context) error {
		return d.heartbeat(ctx, identity)
	})
}

func (d *Daemon) snapshotLoop(ctx context.Context, identity computerstate.Identity) {
	d.periodicLoop(ctx, d.config.SnapshotInterval, d.placementLogger, "placement.sync", func(ctx context.Context) error {
		return d.syncPlacements(ctx, identity)
	})
}

func (d *Daemon) periodicLoop(ctx context.Context, interval time.Duration, logger *observability.Logger, event string, operation func(context.Context) error) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	var failures uint
	var lastDelay time.Duration
	var lastError string
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
				if failures > 0 {
					logger.Info("periodic operation recovered", "event", event+".recovered", "failed_attempts", failures)
				}
				failures = 0
				lastDelay = 0
				lastError = ""
				logger.Debug("periodic operation completed", "event", event+".completed", "next_in", interval)
			} else {
				failures++
				delay = retryDelay(failures, interval, d.config.BackoffMax, d.config.RetryJitter)
				failure := err.Error()
				fields := []any{"event", event + ".failed", "err", err, "attempt", failures, "retry_in", delay}
				if failures == 1 || delay != lastDelay || failure != lastError {
					logger.Warn("periodic operation failed; retry scheduled", fields...)
				} else {
					logger.Debug("periodic operation remains unavailable", fields...)
				}
				lastDelay = delay
				lastError = failure
			}
			timer.Reset(delay)
		}
	}
}

func (d *Daemon) heartbeat(ctx context.Context, identity computerstate.Identity) error {
	inventory, err := d.capabilityInventory()
	if err != nil {
		return fmt.Errorf("build heartbeat capability inventory: %w", err)
	}
	rpcCtx, cancel := d.rpcContext(ctx)
	defer cancel()
	_, err = d.computers.HeartbeatComputer(rpcCtx, connect.NewRequest(&computerv1.HeartbeatComputerRequest{
		ComputerId: identity.ComputerID, RegistrationKey: identity.RegistrationKey,
		CapabilityInventory: inventory,
	}))
	if err != nil {
		return fmt.Errorf("heartbeat computer: %w", err)
	}
	return nil
}

func (d *Daemon) capabilityInventory() (*computerv1.CapabilityInventoryDeclaration, error) {
	if d.config.CapabilityInventory != nil {
		return proto.Clone(d.config.CapabilityInventory).(*computerv1.CapabilityInventoryDeclaration), nil
	}
	return CapabilityInventory()
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
	d.placementLogger.Debug("placement snapshot received", "event", "placement.snapshot.received", "computer_id", identity.ComputerID, "count", len(placements))
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
	ready := make(map[string]uint64)
	for _, placement := range placements {
		if placement.GetState() == placementv1.PlacementState_PLACEMENT_STATE_READY {
			ready[placement.GetAgentId()] = placement.GetDesiredRevision()
		}
	}
	var syncErrors []error
	for _, placement := range placements {
		switch placement.GetState() {
		case placementv1.PlacementState_PLACEMENT_STATE_PENDING:
			placement, err = d.provisionPlacement(ctx, identity, placement)
			if err != nil {
				syncErrors = append(syncErrors, err)
				continue
			}
		case placementv1.PlacementState_PLACEMENT_STATE_READY:
		case placementv1.PlacementState_PLACEMENT_STATE_FAILED:
		}
		if err := validatePlacement(placement, identity.ComputerID); err != nil {
			syncErrors = append(syncErrors, err)
			continue
		}
		if placement.GetState() == placementv1.PlacementState_PLACEMENT_STATE_READY {
			ready[placement.GetAgentId()] = placement.GetDesiredRevision()
			if err := d.reconcileRuntimeSlot(placement); err != nil {
				syncErrors = append(syncErrors, err)
				continue
			}
			if err := d.ensureRuntime(ctx, identity, placement.GetAgentId(), placement.GetDesiredRevision()); err != nil {
				syncErrors = append(syncErrors, err)
			}
		}
	}
	d.config.RuntimeSupervisor.RemoveExcept(ready)
	sessions, err := d.config.State.RuntimeSessions(ctx)
	if err != nil {
		return fmt.Errorf("list local runtime sessions: %w", err)
	}
	for _, session := range sessions {
		rev, current := ready[session.AgentID]
		if current && rev == session.PlacementDesiredRevision && session.ComputerID == identity.ComputerID {
			continue
		}
		if err := d.config.State.DeleteRuntimeSession(ctx, session.AgentID, session.ComputerID, session.PlacementDesiredRevision); err != nil {
			syncErrors = append(syncErrors, fmt.Errorf("delete stale runtime session for agent %q: %w", session.AgentID, err))
		} else {
			d.runtimeLogger.Info("stale runtime session removed", "event", "runtime.session.removed", "agent_id", session.AgentID, "computer_id", session.ComputerID, "placement_desired_revision", session.PlacementDesiredRevision, "reason", "placement_changed")
		}
		d.config.RuntimeSupervisor.Stop(session.AgentID, session.PlacementDesiredRevision)
	}
	return errors.Join(syncErrors...)
}

func validatePlacement(placement *placementv1.AgentPlacement, computerID string) error {
	if placement == nil || placement.GetComputerId() != computerID || placement.GetDesiredRevision() == 0 ||
		placement.GetAgentProfileRevision() == 0 || placement.GetAgentProfile() == nil ||
		placement.GetAgentProfile().GetAgentId() != placement.GetAgentId() ||
		placement.GetAgentProfile().GetRevision() != placement.GetAgentProfileRevision() || placement.GetRuntimeSpec() == nil ||
		placement.GetRuntimeSpec().GetAgentId() != placement.GetAgentId() || placement.GetRuntimeSpec().GetRevision() == 0 {
		return errors.New("placement binding is invalid")
	}
	if _, err := uuid.Parse(placement.GetAgentId()); err != nil {
		return fmt.Errorf("placement agent ID is invalid: %w", err)
	}
	switch placement.GetState() {
	case placementv1.PlacementState_PLACEMENT_STATE_PENDING,
		placementv1.PlacementState_PLACEMENT_STATE_READY,
		placementv1.PlacementState_PLACEMENT_STATE_FAILED:
		return nil
	default:
		return errors.New("placement state is invalid")
	}
}

func (d *Daemon) provisionPlacement(ctx context.Context, identity computerstate.Identity, placement *placementv1.AgentPlacement) (*placementv1.AgentPlacement, error) {
	d.placementLogger.Info("placement provisioning started", "event", "placement.provision.started", "agent_id", placement.GetAgentId(), "computer_id", identity.ComputerID, "desired_revision", placement.GetDesiredRevision())
	provisionErr := d.reconcileRuntimeSlot(placement)
	result := placementv1.AcknowledgementResult_ACKNOWLEDGEMENT_RESULT_READY
	errorCode := ""
	if provisionErr != nil {
		result = placementv1.AcknowledgementResult_ACKNOWLEDGEMENT_RESULT_FAILED
		if _, ok := provisionErr.(*workspace.ProvisionError); ok {
			errorCode = workspace.ErrorCode(provisionErr)
		} else {
			errorCode = computerruntime.ErrorCode(provisionErr)
		}
	}
	rpcCtx, cancel := d.rpcContext(ctx)
	defer cancel()
	response, err := d.placements.AcknowledgeAgentPlacement(rpcCtx, connect.NewRequest(&placementv1.AcknowledgeAgentPlacementRequest{
		ComputerId: identity.ComputerID, RegistrationKey: identity.RegistrationKey,
		AgentId: placement.GetAgentId(), DesiredRevision: placement.GetDesiredRevision(), Result: result, ErrorCode: errorCode,
	}))
	if err != nil {
		return nil, fmt.Errorf("acknowledge placement for agent %q: %w", placement.GetAgentId(), err)
	}
	if response == nil {
		return nil, errors.New("acknowledge placement returned no response")
	}
	acknowledged := response.Msg.GetPlacement()
	d.placementLogger.Info("placement provisioning acknowledged", "event", "placement.provision.acknowledged", "agent_id", placement.GetAgentId(), "computer_id", identity.ComputerID, "desired_revision", placement.GetDesiredRevision(), "state", acknowledged.GetState().String(), "error_code", errorCode)
	return acknowledged, nil
}

func (d *Daemon) reconcileRuntimeSlot(placement *placementv1.AgentPlacement) error {
	layout, err := workspace.ProvisionLayout(d.config.DataRoot, placement.GetAgentId())
	if err != nil {
		return err
	}
	return d.config.RuntimeSupervisor.Reconcile(computerruntime.SlotConfig{
		AgentID: placement.GetAgentId(), ComputerID: placement.GetComputerId(), PlacementDesiredRevision: placement.GetDesiredRevision(),
		AgentProfile: placement.GetAgentProfile(), RuntimeSpec: placement.GetRuntimeSpec(), Workspace: layout.Workspace, Home: layout.Home,
		Temp: layout.Temp, Cache: layout.Cache,
	})
}

func (d *Daemon) ensureRuntime(ctx context.Context, identity computerstate.Identity, agentID string, rev uint64) error {
	now := d.config.Now()
	session, found, err := d.config.State.RuntimeSession(ctx, agentID)
	if err != nil {
		return fmt.Errorf("read runtime session for agent %q: %w", agentID, err)
	}
	if found && session.ComputerID == identity.ComputerID && session.PlacementDesiredRevision == rev && session.ExpiresAt.After(now.Add(d.config.RuntimeRenewBefore)) {
		return nil
	}
	if found && session.ComputerID == identity.ComputerID && session.PlacementDesiredRevision == rev {
		rpcCtx, cancel := d.rpcContext(ctx)
		request := runtimeRequest(session.Token, &runtimev1.RenewSessionRequest{
			ComputerId: identity.ComputerID, RegistrationKey: identity.RegistrationKey,
		})
		response, renewErr := d.runtimes.RenewSession(rpcCtx, request)
		cancel()
		if renewErr == nil && response != nil {
			if err := d.saveRuntimeResponse(ctx, response.Msg.GetSession(), agentID, identity.ComputerID, rev, now); err != nil {
				return err
			}
			d.runtimeLogger.Info("runtime session renewed", "event", "runtime.session.renewed", "agent_id", agentID, "computer_id", identity.ComputerID, "placement_desired_revision", rev, "expires_at", response.Msg.GetSession().GetExpiresAt().AsTime())
			return nil
		}
		if renewErr == nil {
			return errors.New("renew runtime session returned no response")
		}
		if connect.CodeOf(renewErr) != connect.CodeUnauthenticated {
			return fmt.Errorf("renew runtime session for agent %q: %w", agentID, renewErr)
		}
		if err := d.config.State.DeleteRuntimeSession(ctx, agentID, identity.ComputerID, rev); err != nil {
			return fmt.Errorf("delete rejected runtime session for agent %q: %w", agentID, err)
		}
		d.runtimeLogger.Warn("runtime session rejected and removed", "event", "runtime.session.rejected", "agent_id", agentID, "computer_id", identity.ComputerID, "placement_desired_revision", rev)
	}
	rpcCtx, cancel := d.rpcContext(ctx)
	response, err := d.runtimes.CreateSession(rpcCtx, connect.NewRequest(&runtimev1.CreateSessionRequest{
		ComputerId: identity.ComputerID, RegistrationKey: identity.RegistrationKey,
		AgentId: agentID, PlacementDesiredRevision: rev,
	}))
	cancel()
	if err != nil {
		return fmt.Errorf("create runtime session for agent %q: %w", agentID, err)
	}
	if response == nil {
		return errors.New("create runtime session returned no response")
	}
	if err := d.saveRuntimeResponse(ctx, response.Msg.GetSession(), agentID, identity.ComputerID, rev, now); err != nil {
		return err
	}
	d.runtimeLogger.Info("runtime session created", "event", "runtime.session.created", "agent_id", agentID, "computer_id", identity.ComputerID, "placement_desired_revision", rev, "expires_at", response.Msg.GetSession().GetExpiresAt().AsTime())
	return nil
}

func (d *Daemon) saveRuntimeResponse(ctx context.Context, session *runtimev1.Session, agentID, computerID string, rev uint64, updatedAt time.Time) error {
	if session == nil || session.GetAgentId() != agentID || session.GetComputerId() != computerID ||
		session.GetPlacementDesiredRevision() != rev || session.GetExpiresAt() == nil ||
		session.GetExpiresAt().CheckValid() != nil || !session.GetExpiresAt().AsTime().After(updatedAt) ||
		!canonicalSecret(session.GetToken()) {
		return errors.New("runtime session response is invalid")
	}
	if err := d.config.State.SaveRuntimeSession(ctx, computerstate.RuntimeSession{
		AgentID: session.GetAgentId(), ComputerID: session.GetComputerId(),
		PlacementDesiredRevision: session.GetPlacementDesiredRevision(), Token: session.GetToken(),
		ExpiresAt: session.GetExpiresAt().AsTime(), UpdatedAt: updatedAt,
	}); err != nil {
		return fmt.Errorf("save runtime session for agent %q: %w", agentID, err)
	}
	return nil
}
