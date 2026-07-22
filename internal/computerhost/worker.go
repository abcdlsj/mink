package computerhost

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	deliveryv1 "github.com/abcdlsj/sumi/gen/go/sumi/delivery/v1"
	"github.com/abcdlsj/sumi/internal/computerstate"
	"github.com/abcdlsj/sumi/internal/workspace"
	"github.com/google/uuid"
)

func (d *Daemon) startWorker(parent context.Context, session computerstate.RuntimeSession, delivery *deliveryv1.Delivery, run *deliveryv1.Run, launch *deliveryv1.RunLaunch, trigger triggerContext) {
	if validateLaunch(launch, run.GetId(), session.AgentID, session.ComputerID, session.PlacementGeneration) != nil {
		return
	}
	completed, err := d.config.State.HasOutboxCompletion(parent, run.GetId(), launch.GetId(), launch.GetFence())
	if err != nil {
		return
	}
	if completed {
		return
	}
	d.workersMu.Lock()
	if existing, found := d.workers[run.GetId()]; found {
		if existing.launchID == launch.GetId() && existing.fence == launch.GetFence() {
			d.workersMu.Unlock()
			return
		}
		existing.cancel()
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
	go func() {
		defer d.workersWG.Done()
		d.runLeaseWatchdog(ctx, session, delivery, run, launch, trigger, leaseExpiry)
	}()
}

func (d *Daemon) runLeaseWatchdog(ctx context.Context, session computerstate.RuntimeSession, delivery *deliveryv1.Delivery, run *deliveryv1.Run, launch *deliveryv1.RunLaunch, trigger triggerContext, leaseExpiry *atomic.Int64) {
	defer d.removeWorker(run.GetId(), launch.GetId(), launch.GetFence())
	workspacePath, err := workspace.Provision(d.config.DataRoot, session.AgentID)
	if err != nil {
		return
	}
	result := make(chan struct {
		completion Completion
		err        error
	}, 1)
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
			return
		}
		expiry := time.NewTimer(remaining)
		select {
		case <-ctx.Done():
			expiry.Stop()
			return
		case <-expiry.C:
			if updatedExpiry := time.Unix(0, leaseExpiry.Load()); updatedExpiry.After(d.config.Now()) {
				continue
			}
			return
		case executionResult := <-result:
			expiry.Stop()
			if ctx.Err() != nil || !expiresAt.After(d.config.Now()) || executionResult.err != nil || validateCompletion(executionResult.completion) != nil {
				return
			}
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
			if status != enqueueNeedsRetry {
				return
			}
			if !scheduleEnqueueRetry() {
				return
			}
		case <-enqueueRetry:
			expiry.Stop()
			enqueueRetry = nil
			status := d.tryEnqueueCompletion(ctx, expiresAt, run.GetId(), launch.GetId(), launch.GetFence(), *pendingEvent)
			if status != enqueueNeedsRetry {
				return
			}
			if !scheduleEnqueueRetry() {
				return
			}
		case <-renew.C:
			expiry.Stop()
			currentSession, found, err := d.config.State.RuntimeSession(ctx, session.AgentID)
			if err != nil || !found || currentSession.ComputerID != session.ComputerID || currentSession.PlacementGeneration != session.PlacementGeneration {
				return
			}
			updated, err := d.renewRun(ctx, currentSession, run.GetId(), launch.GetId(), launch.GetFence(), expiresAt)
			if err == nil {
				expiresAt = updated
				leaseExpiry.Store(updated.UnixNano())
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
		}
	}
}

func (d *Daemon) stopAllWorkers() {
	d.workersMu.Lock()
	defer d.workersMu.Unlock()
	for runID, worker := range d.workers {
		worker.cancel()
		delete(d.workers, runID)
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
