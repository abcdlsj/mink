package host

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	runv1 "github.com/abcdlsj/sumi/gen/go/sumi/run/v1"
	computerruntime "github.com/abcdlsj/sumi/internal/computer/runtime"
	computerstate "github.com/abcdlsj/sumi/internal/computer/state"
	"github.com/google/uuid"
)

func (d *Daemon) startRunWorker(parent context.Context, session computerstate.RuntimeSession, run *runv1.Run, trigger triggerContext) {
	if err := validateRunningRun(run, session); err != nil {
		d.runLogger.Warn("run worker rejected invalid binding", "event", "run.worker.binding.invalid", "agent_id", session.AgentID, "run_id", run.GetId())
		return
	}
	d.workers.Add(1)
	go func() {
		defer d.workers.Done()
		d.runWorker(parent, session, run, trigger)
	}()
}

func (d *Daemon) runWorker(parent context.Context, session computerstate.RuntimeSession, run *runv1.Run, trigger triggerContext) {
	lease, err := d.config.RuntimeSupervisor.Acquire(parent, session.AgentID, session.PlacementDesiredRevision)
	if errors.Is(err, computerruntime.ErrBusy) {
		return
	}
	if err != nil {
		d.engineLogger.Warn("runtime slot acquisition failed", "event", "engine.slot.acquire.failed", "agent_id", session.AgentID, "run_id", run.GetId())
		return
	}
	defer lease.Close()
	ctx := lease.Context()
	completed, err := d.config.State.HasOutboxCompletion(ctx, run.GetId(), run.GetAttempt(), run.GetFence())
	if err != nil || completed {
		return
	}
	journal := computerstate.RunJournal{
		AgentID: session.AgentID, PlacementDesiredRevision: session.PlacementDesiredRevision,
		RunID: run.GetId(), Attempt: run.GetAttempt(), Fence: run.GetFence(), State: "running", StartedAt: d.config.Now(),
	}
	if err := d.config.State.StartRun(ctx, journal); err != nil {
		d.runLogger.Warn("run journal persistence failed", "event", "run.journal.start.failed", "agent_id", session.AgentID, "run_id", run.GetId())
		return
	}
	finished := false
	defer func() {
		if !finished {
			state := "cancelled"
			if ctx.Err() == nil {
				state = "failed"
			}
			_ = d.config.State.FinishRun(context.WithoutCancel(parent), session.AgentID, run.GetId(), run.GetAttempt(), run.GetFence(), state, d.config.Now())
		}
	}()

	result := make(chan struct {
		completion Completion
		err        error
	}, 1)
	go func() {
		completion, executeErr := lease.Execute(Execution{
			ComputerID: session.ComputerID, RunID: run.GetId(), Attempt: run.GetAttempt(), Fence: run.GetFence(),
			SpaceID: trigger.spaceID, ThreadRootMessageID: trigger.threadRootMessageID,
			BasisTargetSequence: run.GetInputBasisTargetSequence(), Messages: trigger.messages, CurrentInput: trigger.body,
		})
		result <- struct {
			completion Completion
			err        error
		}{completion, executeErr}
	}()

	expiresAt := run.GetLeaseExpiresAt().AsTime()
	renew := time.NewTicker(d.config.RunRenewInterval)
	defer renew.Stop()
	var pending *computerstate.OutboxEvent
	var retry <-chan time.Time
	var retryTimer *time.Timer
	defer func() {
		if retryTimer != nil {
			retryTimer.Stop()
		}
	}()
	for {
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
			return
		case executionResult := <-result:
			expiry.Stop()
			completion := executionResult.completion
			if executionResult.err != nil {
				completion = Completion{Outcome: computerruntime.OutcomeFailed, ErrorCode: "engine_execution_failed", Body: "Run execution failed before producing a result."}
			}
			if err := validateCompletion(completion); err != nil {
				completion = Completion{Outcome: computerruntime.OutcomeFailed, ErrorCode: "engine_completion_invalid", Body: "Run execution returned an invalid result."}
			}
			event := computerstate.OutboxEvent{
				OutboxEventID: uuid.NewString(), RequestID: uuid.NewString(), AgentID: session.AgentID,
				PlacementDesiredRevision: session.PlacementDesiredRevision, RunID: run.GetId(), Attempt: run.GetAttempt(),
				Fence: run.GetFence(), Outcome: outcomeName(completion.Outcome), ErrorCode: completion.ErrorCode,
				Body: completion.Body, MentionedAgentIDs: completion.MentionedAgentIDs,
				UsageInputUnits: completion.Usage.InputUnits, UsageOutputUnits: completion.Usage.OutputUnits, CreatedAt: d.config.Now(),
			}
			pending = &event
			result = nil
			if d.tryEnqueueCompletion(ctx, expiresAt, event) {
				_ = d.config.State.FinishRun(context.WithoutCancel(parent), session.AgentID, run.GetId(), run.GetAttempt(), run.GetFence(), "completed", d.config.Now())
				finished = true
				return
			}
			retryTimer = time.NewTimer(d.config.OutboxInterval)
			retry = retryTimer.C
		case <-retry:
			expiry.Stop()
			retry = nil
			if pending != nil && d.tryEnqueueCompletion(ctx, expiresAt, *pending) {
				_ = d.config.State.FinishRun(context.WithoutCancel(parent), session.AgentID, run.GetId(), run.GetAttempt(), run.GetFence(), "completed", d.config.Now())
				finished = true
				return
			}
			retryTimer = time.NewTimer(d.config.OutboxInterval)
			retry = retryTimer.C
		case <-renew.C:
			expiry.Stop()
			currentSession, found, err := d.config.State.RuntimeSession(ctx, session.AgentID)
			if err != nil || !found || currentSession.ComputerID != session.ComputerID || currentSession.PlacementDesiredRevision != session.PlacementDesiredRevision {
				return
			}
			updated, err := d.renewRun(ctx, currentSession, run, expiresAt)
			if err == nil {
				expiresAt = updated
				continue
			}
			code := connect.CodeOf(err)
			if code == connect.CodeFailedPrecondition || code == connect.CodeUnauthenticated || code == connect.CodeNotFound {
				return
			}
		}
	}
}

func (d *Daemon) renewRun(ctx context.Context, session computerstate.RuntimeSession, run *runv1.Run, previousExpiry time.Time) (time.Time, error) {
	subjectID := fmt.Sprintf("%s:%d:%d", run.GetId(), run.GetAttempt(), run.GetFence())
	payloadHash := mutationHash(string(computerstate.MutationRunRenew), subjectID, session.ComputerID, fmt.Sprint(session.PlacementDesiredRevision))
	attempt, err := d.config.State.BeginMutation(ctx, computerstate.MutationAttempt{
		Operation: computerstate.MutationRunRenew, SubjectID: subjectID, PayloadHash: payloadHash,
		RunID: run.GetId(), Attempt: run.GetAttempt(), Fence: run.GetFence(), CreatedAt: d.config.Now(),
	})
	if err != nil {
		return time.Time{}, fmt.Errorf("begin run renewal: %w", err)
	}
	rpcCtx, cancel := d.rpcContext(ctx)
	response, err := d.runs.RenewRun(rpcCtx, runtimeRequest(session.Token, &runv1.RenewRunRequest{
		RequestId: attempt.RequestID, RunId: run.GetId(), Attempt: run.GetAttempt(), Fence: run.GetFence(),
	}))
	cancel()
	if err != nil {
		d.invalidateRuntimeOnUnauthenticated(ctx, session, err)
		d.finishCanonicalMutation(ctx, attempt.RequestID, err)
		return time.Time{}, fmt.Errorf("renew run: %w", err)
	}
	if response == nil || response.Msg.GetRun() == nil {
		return time.Time{}, errors.New("renew run returned no response")
	}
	updated := response.Msg.GetRun()
	if err := validateRunningRun(updated, session); err != nil {
		return time.Time{}, err
	}
	if updated.GetId() != run.GetId() || updated.GetAttempt() != run.GetAttempt() || updated.GetFence() != run.GetFence() ||
		!updated.GetLeaseExpiresAt().AsTime().After(previousExpiry) {
		return time.Time{}, errors.New("renewed run does not advance the current lease")
	}
	expiresAt := updated.GetLeaseExpiresAt().AsTime()
	if err := d.config.State.CompleteMutation(ctx, attempt.RequestID, computerstate.MutationSucceeded, updated.GetAttempt(), updated.GetFence(), &expiresAt, d.config.Now()); err != nil {
		return time.Time{}, fmt.Errorf("complete run renewal: %w", err)
	}
	return expiresAt, nil
}

func (d *Daemon) tryEnqueueCompletion(ctx context.Context, expiresAt time.Time, event computerstate.OutboxEvent) bool {
	if ctx.Err() != nil || !expiresAt.After(d.config.Now()) {
		return false
	}
	persist := d.persistOutbox
	if persist == nil {
		persist = d.config.State.EnqueueOutbox
	}
	return persist(ctx, event) == nil
}
