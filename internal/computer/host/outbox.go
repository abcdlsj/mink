package host

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	deliveryv1 "github.com/abcdlsj/sumi/gen/go/sumi/delivery/v1"
	computerstate "github.com/abcdlsj/sumi/internal/computer/state"
)

func (d *Daemon) outboxLoop(ctx context.Context) {
	d.periodicLoop(ctx, d.config.OutboxInterval, d.dispatchOutbox)
}

func (d *Daemon) dispatchOutbox(ctx context.Context) error {
	events, err := d.config.State.PendingOutbox(ctx, 100)
	if err != nil {
		return fmt.Errorf("list outbox events: %w", err)
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
		response, completeErr := d.deliveries.CompleteRun(rpcCtx, runtimeRequest(session.Token, &deliveryv1.CompleteRunRequest{
			RequestId: event.RequestID, OutboxEventId: event.OutboxEventID,
			RunId: event.RunID, LaunchId: event.LaunchID, Fence: event.Fence,
			Outcome: outcomeValue(event.Outcome), Body: event.Body, MentionedAgentIds: event.MentionedAgentIDs,
		}))
		cancel()
		if completeErr == nil && response != nil && validateCompleteResponse(response.Msg, event) == nil {
			if err := d.config.State.AckOutbox(ctx, event.OutboxEventID); err != nil {
				dispatchErrors = append(dispatchErrors, fmt.Errorf("ack outbox event %q: %w", event.OutboxEventID, err))
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
			}
		} else {
			dispatchErrors = append(dispatchErrors, fmt.Errorf("complete outbox event %q: %w", event.OutboxEventID, completeErr))
		}
	}
	return errors.Join(dispatchErrors...)
}

func (d *Daemon) finishCanonicalMutation(ctx context.Context, requestID string, rpcErr error) {
	code := connect.CodeOf(rpcErr)
	if code == connect.CodeUnavailable || code == connect.CodeDeadlineExceeded || code == connect.CodeCanceled ||
		code == connect.CodeUnknown || code == connect.CodeUnauthenticated {
		return
	}
	_ = d.config.State.CompleteMutation(ctx, requestID, computerstate.MutationFailed, "", 0, nil, d.config.Now())
}
