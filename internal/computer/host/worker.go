package host

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	deliveryv1 "github.com/abcdlsj/sumi/gen/go/sumi/delivery/v1"
	computerstate "github.com/abcdlsj/sumi/internal/computer/state"
	"github.com/abcdlsj/sumi/internal/workspace"
	"github.com/google/uuid"
)

func (d *Daemon) startWorker(parent context.Context, session computerstate.RuntimeSession, delivery *deliveryv1.Delivery, run *deliveryv1.Run, launch *deliveryv1.RunLaunch, trigger triggerContext) {
	if err := validateLaunch(launch, run.GetId(), session.AgentID, session.ComputerID, session.PlacementGeneration); err != nil {
		d.driverLogger.Warn("worker rejected invalid launch", "event", "driver.worker.launch.invalid", "agent_id", session.AgentID, "run_id", run.GetId(), "err", err)
		return
	}
	completed, err := d.config.State.HasOutboxCompletion(parent, run.GetId(), launch.GetId(), launch.GetFence())
	if err != nil {
		d.outboxLogger.Warn("worker could not inspect completion state", "event", "outbox.completion.inspect.failed", "run_id", run.GetId(), "launch_id", launch.GetId(), "fence", launch.GetFence(), "err", err)
		return
	}
	if completed {
		d.outboxLogger.Debug("worker skipped durable completion", "event", "outbox.completion.already_stored", "run_id", run.GetId(), "launch_id", launch.GetId(), "fence", launch.GetFence())
		return
	}
	d.workersMu.Lock()
	if existing, found := d.workers[run.GetId()]; found {
		if existing.launchID == launch.GetId() && existing.fence == launch.GetFence() {
			d.workersMu.Unlock()
			d.driverLogger.Debug("worker already running", "event", "driver.worker.duplicate_ignored", "run_id", run.GetId(), "launch_id", launch.GetId(), "fence", launch.GetFence())
			return
		}
		existing.cancel()
		d.driverLogger.Warn("worker replaced by newer launch", "event", "driver.worker.replaced", "run_id", run.GetId(), "old_launch_id", existing.launchID, "old_fence", existing.fence, "launch_id", launch.GetId(), "fence", launch.GetFence())
	}
	ctx, cancel := context.WithCancel(parent)
	leaseExpiry := &atomic.Int64{}
	leaseExpiry.Store(launch.GetExpiresAt().AsTime().UnixNano())
	d.workers[run.GetId()] = runWorker{
		cancel: cancel, agentID: session.AgentID, generation: session.PlacementGeneration,
		launchID: launch.GetId(), fence: launch.GetFence(), leaseExpiry: leaseExpiry,
	}
	d.workersWG.Add(1)
	d.workersMu.Unlock()
	d.driverLogger.Info("worker started", "event", "driver.worker.started", "agent_id", session.AgentID, "run_id", run.GetId(), "launch_id", launch.GetId(), "fence", launch.GetFence(), "placement_generation", session.PlacementGeneration, "lease_expires_at", launch.GetExpiresAt().AsTime())
	go func() {
		defer d.workersWG.Done()
		d.runLeaseWatchdog(ctx, session, delivery, run, launch, trigger, leaseExpiry)
	}()
}

func (d *Daemon) runLeaseWatchdog(ctx context.Context, session computerstate.RuntimeSession, delivery *deliveryv1.Delivery, run *deliveryv1.Run, launch *deliveryv1.RunLaunch, trigger triggerContext, leaseExpiry *atomic.Int64) {
	defer d.removeWorker(run.GetId(), launch.GetId(), launch.GetFence())
	workspacePath, err := workspace.Provision(d.config.DataRoot, session.AgentID)
	if err != nil {
		d.driverLogger.Error("worker workspace provisioning failed", "event", "driver.workspace.provision.failed", "agent_id", session.AgentID, "run_id", run.GetId(), "launch_id", launch.GetId(), "err", err)
		return
	}
	result := make(chan struct {
		completion Completion
		err        error
	}, 1)
	executionStarted := d.config.Now()
	go func() {
		completion, executeErr := d.config.Executor.Execute(ctx, Execution{
			AgentID: session.AgentID, ComputerID: session.ComputerID, DeliveryID: delivery.GetId(), RunID: run.GetId(),
			LaunchID: launch.GetId(), Fence: launch.GetFence(), PlacementGeneration: session.PlacementGeneration,
			Workspace: workspacePath, SpaceID: trigger.spaceID, ThreadRootMessageID: trigger.threadRootMessageID,
			BasisTargetSequence: run.GetBasisTargetSequence(), CurrentInput: trigger.body,
		})
		result <- struct {
			completion Completion
			err        error
		}{completion, executeErr}
	}()
	expiresAt := launch.GetExpiresAt().AsTime()
	renew := time.NewTicker(d.config.RunRenewInterval)
	defer renew.Stop()
	var pendingEvent *computerstate.OutboxEvent
	var enqueueTimer *time.Timer
	var enqueueRetry <-chan time.Time
	var enqueueFailures uint
	defer func() {
		if enqueueTimer != nil {
			enqueueTimer.Stop()
		}
	}()
	scheduleEnqueueRetry := func() bool {
		enqueueFailures++
		delay := retryDelay(enqueueFailures, d.config.OutboxInterval, d.config.BackoffMax, d.config.RetryJitter)
		remaining := expiresAt.Sub(d.config.Now())
		if remaining <= 0 {
			return false
		}
		if delay > remaining {
			delay = remaining
		}
		enqueueTimer = time.NewTimer(delay)
		enqueueRetry = enqueueTimer.C
		return true
	}
	for {
		if updatedExpiry := time.Unix(0, leaseExpiry.Load()); updatedExpiry.After(expiresAt) {
			expiresAt = updatedExpiry
		}
		remaining := expiresAt.Sub(d.config.Now())
		if remaining <= 0 {
			d.driverLogger.Warn("worker lease expired", "event", "driver.worker.lease_expired", "agent_id", session.AgentID, "run_id", run.GetId(), "launch_id", launch.GetId(), "fence", launch.GetFence())
			return
		}
		expiry := time.NewTimer(remaining)
		select {
		case <-ctx.Done():
			expiry.Stop()
			d.driverLogger.Info("worker canceled", "event", "driver.worker.canceled", "agent_id", session.AgentID, "run_id", run.GetId(), "launch_id", launch.GetId(), "fence", launch.GetFence(), "reason", context.Cause(ctx))
			return
		case <-expiry.C:
			if updatedExpiry := time.Unix(0, leaseExpiry.Load()); updatedExpiry.After(d.config.Now()) {
				continue
			}
			d.driverLogger.Warn("worker lease watchdog expired", "event", "driver.worker.lease_expired", "agent_id", session.AgentID, "run_id", run.GetId(), "launch_id", launch.GetId(), "fence", launch.GetFence())
			return
		case executionResult := <-result:
			expiry.Stop()
			if ctx.Err() != nil {
				d.driverLogger.Info("driver execution canceled", "event", "driver.execution.canceled", "agent_id", session.AgentID, "run_id", run.GetId(), "launch_id", launch.GetId(), "duration", d.config.Now().Sub(executionStarted), "reason", context.Cause(ctx))
				return
			}
			if !expiresAt.After(d.config.Now()) {
				d.driverLogger.Warn("driver execution finished after lease expiry", "event", "driver.execution.stale", "agent_id", session.AgentID, "run_id", run.GetId(), "launch_id", launch.GetId(), "duration", d.config.Now().Sub(executionStarted))
				return
			}
			if executionResult.err != nil {
				d.driverLogger.Warn("driver execution failed", "event", "driver.execution.failed", "agent_id", session.AgentID, "run_id", run.GetId(), "launch_id", launch.GetId(), "duration", d.config.Now().Sub(executionStarted), "err", executionResult.err)
				return
			}
			if err := validateCompletion(executionResult.completion); err != nil {
				d.driverLogger.Warn("driver returned invalid completion", "event", "driver.completion.invalid", "agent_id", session.AgentID, "run_id", run.GetId(), "launch_id", launch.GetId(), "err", err)
				return
			}
			d.driverLogger.Info("driver execution completed", "event", "driver.execution.completed", "agent_id", session.AgentID, "run_id", run.GetId(), "launch_id", launch.GetId(), "duration", d.config.Now().Sub(executionStarted), "outcome", outcomeName(executionResult.completion.Outcome), "mentioned_agents", len(executionResult.completion.MentionedAgentIDs))
			event := computerstate.OutboxEvent{
				OutboxEventID: uuid.NewString(), RequestID: uuid.NewString(), AgentID: session.AgentID,
				PlacementGeneration: session.PlacementGeneration, RunID: run.GetId(), LaunchID: launch.GetId(),
				Fence: launch.GetFence(), Outcome: outcomeName(executionResult.completion.Outcome),
				Body: executionResult.completion.Body, MentionedAgentIDs: executionResult.completion.MentionedAgentIDs,
				CreatedAt: d.config.Now(),
			}
			pendingEvent = &event
			result = nil
			status := d.tryEnqueueCompletion(ctx, expiresAt, run.GetId(), launch.GetId(), launch.GetFence(), event)
			if status == enqueueStored {
				d.outboxLogger.Info("run completion stored in outbox", "event", "outbox.completion.stored", "outbox_event_id", event.OutboxEventID, "agent_id", session.AgentID, "run_id", run.GetId(), "launch_id", launch.GetId(), "fence", launch.GetFence())
				return
			}
			if status == enqueueStale {
				d.outboxLogger.Warn("run completion became stale before persistence", "event", "outbox.completion.stale", "agent_id", session.AgentID, "run_id", run.GetId(), "launch_id", launch.GetId(), "fence", launch.GetFence())
				return
			}
			d.outboxLogger.Warn("run completion persistence failed; retry scheduled", "event", "outbox.completion.store.failed", "outbox_event_id", event.OutboxEventID, "run_id", run.GetId(), "launch_id", launch.GetId(), "fence", launch.GetFence(), "attempt", enqueueFailures+1)
			if !scheduleEnqueueRetry() {
				d.outboxLogger.Error("run completion could not be persisted before lease expiry", "event", "outbox.completion.store.exhausted", "outbox_event_id", event.OutboxEventID, "run_id", run.GetId(), "launch_id", launch.GetId(), "fence", launch.GetFence())
				return
			}
		case <-enqueueRetry:
			expiry.Stop()
			enqueueRetry = nil
			status := d.tryEnqueueCompletion(ctx, expiresAt, run.GetId(), launch.GetId(), launch.GetFence(), *pendingEvent)
			if status == enqueueStored {
				d.outboxLogger.Info("run completion stored in outbox", "event", "outbox.completion.stored", "outbox_event_id", pendingEvent.OutboxEventID, "agent_id", session.AgentID, "run_id", run.GetId(), "launch_id", launch.GetId(), "fence", launch.GetFence(), "attempt", enqueueFailures+1)
				return
			}
			if status == enqueueStale {
				d.outboxLogger.Warn("run completion became stale before retry", "event", "outbox.completion.stale", "outbox_event_id", pendingEvent.OutboxEventID, "run_id", run.GetId(), "launch_id", launch.GetId(), "fence", launch.GetFence())
				return
			}
			d.outboxLogger.Warn("run completion persistence retry failed", "event", "outbox.completion.store.failed", "outbox_event_id", pendingEvent.OutboxEventID, "run_id", run.GetId(), "launch_id", launch.GetId(), "fence", launch.GetFence(), "attempt", enqueueFailures+1)
			if !scheduleEnqueueRetry() {
				d.outboxLogger.Error("run completion could not be persisted before lease expiry", "event", "outbox.completion.store.exhausted", "outbox_event_id", pendingEvent.OutboxEventID, "run_id", run.GetId(), "launch_id", launch.GetId(), "fence", launch.GetFence())
				return
			}
		case <-renew.C:
			expiry.Stop()
			currentSession, found, err := d.config.State.RuntimeSession(ctx, session.AgentID)
			if err != nil || !found || currentSession.ComputerID != session.ComputerID || currentSession.PlacementGeneration != session.PlacementGeneration {
				d.runtimeLogger.Warn("worker runtime binding lost", "event", "runtime.binding.lost", "agent_id", session.AgentID, "run_id", run.GetId(), "placement_generation", session.PlacementGeneration, "err", err)
				return
			}
			updated, err := d.renewRun(ctx, currentSession, run.GetId(), launch.GetId(), launch.GetFence(), expiresAt)
			if err == nil {
				expiresAt = updated
				leaseExpiry.Store(updated.UnixNano())
				d.deliveryLogger.Debug("run lease renewed", "event", "delivery.run.renewed", "agent_id", session.AgentID, "run_id", run.GetId(), "launch_id", launch.GetId(), "fence", launch.GetFence(), "expires_at", updated)
			} else {
				d.deliveryLogger.Warn("run lease renewal failed", "event", "delivery.run.renew.failed", "agent_id", session.AgentID, "run_id", run.GetId(), "launch_id", launch.GetId(), "fence", launch.GetFence(), "err", err)
			}
		}
	}
}

func (d *Daemon) renewRun(ctx context.Context, session computerstate.RuntimeSession, runID, launchID string, fence uint64, previousExpiry time.Time) (time.Time, error) {
	payloadHash := mutationHash(string(computerstate.MutationRunRenew), runID, launchID, fmt.Sprint(fence), session.ComputerID, fmt.Sprint(session.PlacementGeneration))
	attempt, err := d.config.State.BeginMutation(ctx, computerstate.MutationAttempt{
		Operation: computerstate.MutationRunRenew, SubjectID: launchID, PayloadHash: payloadHash,
		RunID: runID, LaunchID: launchID, Fence: fence, CreatedAt: d.config.Now(),
	})
	if err != nil {
		return time.Time{}, fmt.Errorf("begin run renewal: %w", err)
	}
	rpcCtx, cancel := d.rpcContext(ctx)
	response, err := d.deliveries.RenewRun(rpcCtx, runtimeRequest(session.Token, &deliveryv1.RenewRunRequest{
		RequestId: attempt.RequestID, RunId: runID, LaunchId: launchID, Fence: fence,
	}))
	cancel()
	if err != nil {
		d.invalidateRuntimeOnUnauthenticated(ctx, session, err)
		d.finishCanonicalMutation(ctx, attempt.RequestID, err)
		return time.Time{}, fmt.Errorf("renew run: %w", err)
	}
	if response == nil {
		return time.Time{}, errors.New("renew run returned no response")
	}
	updated := response.Msg.GetLaunch()
	if err := validateLaunch(updated, runID, session.AgentID, session.ComputerID, session.PlacementGeneration); err != nil {
		return time.Time{}, err
	}
	if updated.GetId() != launchID || updated.GetFence() != fence || !updated.GetExpiresAt().AsTime().After(previousExpiry) {
		return time.Time{}, errors.New("renewed launch does not advance the current lease")
	}
	expiresAt := updated.GetExpiresAt().AsTime()
	if err := d.config.State.CompleteMutation(ctx, attempt.RequestID, computerstate.MutationSucceeded, launchID, fence, &expiresAt, d.config.Now()); err != nil {
		return time.Time{}, fmt.Errorf("complete run renewal: %w", err)
	}
	return expiresAt, nil
}

func (d *Daemon) stopWorkerBinding(agentID string, generation uint64) {
	d.workersMu.Lock()
	defer d.workersMu.Unlock()
	for runID, worker := range d.workers {
		if worker.agentID == agentID && worker.generation == generation {
			worker.cancel()
			delete(d.workers, runID)
			d.driverLogger.Info("worker stopped for runtime binding", "event", "driver.worker.binding_stopped", "agent_id", agentID, "run_id", runID, "placement_generation", generation)
		}
	}
}

func (d *Daemon) reconcileWorkers(active map[string]uint64) {
	d.workersMu.Lock()
	defer d.workersMu.Unlock()
	for runID, worker := range d.workers {
		generation, current := active[worker.agentID]
		if !current || generation != worker.generation {
			worker.cancel()
			delete(d.workers, runID)
			d.driverLogger.Info("worker stopped for placement change", "event", "driver.worker.placement_stopped", "agent_id", worker.agentID, "run_id", runID, "placement_generation", worker.generation)
		}
	}
}

func (d *Daemon) stopAllWorkers() {
	d.workersMu.Lock()
	defer d.workersMu.Unlock()
	for runID, worker := range d.workers {
		worker.cancel()
		delete(d.workers, runID)
		d.driverLogger.Info("worker stopped during daemon shutdown", "event", "driver.worker.shutdown_stopped", "agent_id", worker.agentID, "run_id", runID, "launch_id", worker.launchID, "fence", worker.fence)
	}
}

func (d *Daemon) removeWorker(runID, launchID string, fence uint64) {
	d.workersMu.Lock()
	defer d.workersMu.Unlock()
	worker, found := d.workers[runID]
	if found && worker.launchID == launchID && worker.fence == fence {
		delete(d.workers, runID)
	}
}

type enqueueStatus uint8

const (
	enqueueStale enqueueStatus = iota
	enqueueStored
	enqueueNeedsRetry
)

func (d *Daemon) tryEnqueueCompletion(ctx context.Context, expiresAt time.Time, runID, launchID string, fence uint64, event computerstate.OutboxEvent) enqueueStatus {
	d.workersMu.Lock()
	defer d.workersMu.Unlock()
	worker, found := d.workers[runID]
	if ctx.Err() != nil || !expiresAt.After(d.config.Now()) || !found || worker.launchID != launchID || worker.fence != fence {
		return enqueueStale
	}
	persist := d.persistOutbox
	if persist == nil {
		persist = d.config.State.EnqueueOutbox
	}
	if err := persist(ctx, event); err != nil {
		return enqueueNeedsRetry
	}
	return enqueueStored
}
