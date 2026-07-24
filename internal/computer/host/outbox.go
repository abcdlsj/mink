package host

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	grantv1 "github.com/abcdlsj/sumi/gen/go/sumi/grant/v1"
	runv1 "github.com/abcdlsj/sumi/gen/go/sumi/run/v1"
	spacev1 "github.com/abcdlsj/sumi/gen/go/sumi/space/v1"
	computerstate "github.com/abcdlsj/sumi/internal/computer/state"
)

func (d *Daemon) outboxLoop(ctx context.Context) {
	d.periodicLoop(ctx, d.config.OutboxInterval, d.outboxLogger, "outbox.dispatch", d.dispatchOutbox)
}

func (d *Daemon) dispatchOutbox(ctx context.Context) error {
	events, err := d.config.State.PendingOutbox(ctx, 100)
	if err != nil {
		return fmt.Errorf("list outbox events: %w", err)
	}
	if len(events) > 0 {
		d.outboxLogger.Debug("outbox batch loaded", "event", "outbox.batch.loaded", "count", len(events))
	}
	var dispatchErrors []error
	for _, event := range events {
		session, found, err := d.config.State.RuntimeSession(ctx, event.AgentID)
		if err != nil || !found || !session.ExpiresAt.After(d.config.Now()) {
			continue
		}
		if err := d.config.State.RecordOutboxAttempt(ctx, event.OutboxEventID, d.config.Now()); err != nil {
			dispatchErrors = append(dispatchErrors, fmt.Errorf("record outbox attempt %q: %w", event.OutboxEventID, err))
			continue
		}
		rpcCtx, cancel := d.rpcContext(ctx)
		response, completeErr := d.runs.CompleteRun(rpcCtx, runtimeRequest(session.Token, &runv1.CompleteRunRequest{
			RequestId: event.RequestID, OutboxEventId: event.OutboxEventID,
			RunId: event.RunID, RunProof: &grantv1.RunProof{RunId: event.RunID, Attempt: event.Attempt, Fence: event.Fence},
			Outcome: outcomeValue(event.Outcome), Body: event.Body, MentionedPrincipals: mentionedAgents(event.MentionedAgentIDs),
			ErrorCode: event.ErrorCode, Usage: &runv1.RunUsage{InputUnits: event.UsageInputUnits, OutputUnits: event.UsageOutputUnits},
		}))
		cancel()
		if completeErr == nil && response != nil && validateCompleteResponse(response.Msg, event) == nil {
			if err := d.config.State.AckOutbox(ctx, event.OutboxEventID); err != nil {
				dispatchErrors = append(dispatchErrors, fmt.Errorf("ack outbox event %q: %w", event.OutboxEventID, err))
			} else {
				d.outboxLogger.Info("run completion delivered", "event", "outbox.completion.acknowledged", "outbox_event_id", event.OutboxEventID, "agent_id", event.AgentID, "run_id", event.RunID, "run_attempt", event.Attempt, "fence", event.Fence, "outbox_attempt", event.Attempts+1)
			}
			continue
		}
		if completeErr == nil {
			dispatchErrors = append(dispatchErrors, fmt.Errorf("complete outbox event %q returned invalid facts", event.OutboxEventID))
			continue
		}
		d.invalidateRuntimeOnUnauthenticated(ctx, session, completeErr)
		code := connect.CodeOf(completeErr)
		if code == connect.CodeFailedPrecondition || code == connect.CodeAlreadyExists {
			if err := d.config.State.TombstoneOutbox(ctx, event.OutboxEventID, code.String()); err != nil {
				dispatchErrors = append(dispatchErrors, fmt.Errorf("tombstone outbox event %q: %w", event.OutboxEventID, err))
			} else {
				d.outboxLogger.Warn("stale run completion tombstoned", "event", "outbox.completion.tombstoned", "outbox_event_id", event.OutboxEventID, "run_id", event.RunID, "run_attempt", event.Attempt, "fence", event.Fence, "code", code.String())
			}
		} else {
			dispatchErrors = append(dispatchErrors, fmt.Errorf("complete outbox event %q: %w", event.OutboxEventID, completeErr))
		}
	}
	return errors.Join(dispatchErrors...)
}

func mentionedAgents(agentIDs []string) []*spacev1.Principal {
	principals := make([]*spacev1.Principal, 0, len(agentIDs))
	for _, agentID := range agentIDs {
		principals = append(principals, &spacev1.Principal{Kind: spacev1.PrincipalKind_PRINCIPAL_KIND_AGENT, Id: agentID})
	}
	return principals
}

func (d *Daemon) finishCanonicalMutation(ctx context.Context, requestID string, rpcErr error) {
	code := connect.CodeOf(rpcErr)
	if code == connect.CodeUnavailable || code == connect.CodeDeadlineExceeded || code == connect.CodeCanceled ||
		code == connect.CodeUnknown || code == connect.CodeUnauthenticated {
		return
	}
	_ = d.config.State.CompleteMutation(ctx, requestID, computerstate.MutationFailed, 0, 0, nil, d.config.Now())
}
