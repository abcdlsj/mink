package host

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	inboxv1 "github.com/abcdlsj/sumi/gen/go/sumi/inbox/v1"
	runv1 "github.com/abcdlsj/sumi/gen/go/sumi/run/v1"
	spacev1 "github.com/abcdlsj/sumi/gen/go/sumi/space/v1"
	computerruntime "github.com/abcdlsj/sumi/internal/computer/runtime"
	computerstate "github.com/abcdlsj/sumi/internal/computer/state"
	"github.com/abcdlsj/sumi/internal/transport/messagecodec"
	"google.golang.org/protobuf/proto"
)

type triggerContext struct {
	spaceID             string
	threadRootMessageID string
	observedHead        uint64
	messages            []computerruntime.ContextMessage
	body                string
}

func (d *Daemon) runLoop(ctx context.Context, identity computerstate.Identity) {
	d.periodicLoop(ctx, d.config.RunInterval, d.runLogger, "run.dispatch", func(ctx context.Context) error {
		return d.dispatchRuns(ctx, identity)
	})
}

func (d *Daemon) dispatchRuns(ctx context.Context, identity computerstate.Identity) error {
	sessions, err := d.config.State.RuntimeSessions(ctx)
	if err != nil {
		return fmt.Errorf("list runtime sessions for run dispatch: %w", err)
	}
	var dispatchErrors []error
	for _, session := range sessions {
		if session.ComputerID != identity.ComputerID || !session.ExpiresAt.After(d.config.Now()) {
			continue
		}
		if err := d.dispatchAgentRun(ctx, session); err != nil {
			dispatchErrors = append(dispatchErrors, fmt.Errorf("dispatch agent %q: %w", session.AgentID, err))
		}
	}
	return errors.Join(dispatchErrors...)
}

func (d *Daemon) dispatchAgentRun(ctx context.Context, session computerstate.RuntimeSession) error {
	rpcCtx, cancel := d.rpcContext(ctx)
	response, err := d.runs.ListRuns(rpcCtx, runtimeRequest(session.Token, &runv1.ListRunsRequest{Limit: 50}))
	cancel()
	if err != nil {
		d.invalidateRuntimeOnUnauthenticated(ctx, session, err)
		return fmt.Errorf("list runs: %w", err)
	}
	if response == nil {
		return errors.New("list runs returned no response")
	}
	var selected *runv1.Run
	for _, candidate := range response.Msg.GetRuns() {
		if err := validateListedRun(candidate, session.AgentID); err != nil {
			return err
		}
		if candidate.GetState() == runv1.RunState_RUN_STATE_RUNNING {
			selected = candidate
			break
		}
		if selected == nil {
			selected = candidate
		}
	}
	if selected == nil {
		return nil
	}
	trigger, err := d.observeRun(ctx, session, selected)
	if err != nil {
		return err
	}
	if selected.GetState() == runv1.RunState_RUN_STATE_QUEUED {
		selected, err = d.claimRun(ctx, session, selected.GetId())
		if err != nil {
			return err
		}
		if selected.GetInputBasisTargetSequence() != trigger.observedHead {
			return errors.New("claimed run input basis does not match observed target")
		}
	} else if err := d.reconcileClaimMutation(ctx, session, selected); err != nil {
		return err
	}
	if err := validateRunningRun(selected, session); err != nil {
		return err
	}
	if !selected.GetLeaseExpiresAt().AsTime().After(d.config.Now()) {
		return nil
	}
	d.startRunWorker(ctx, session, selected, trigger)
	return nil
}

func (d *Daemon) observeRun(ctx context.Context, session computerstate.RuntimeSession, run *runv1.Run) (triggerContext, error) {
	if run == nil || run.GetTarget() == nil {
		return triggerContext{}, errors.New("run target is required")
	}
	rpcCtx, cancel := d.rpcContext(ctx)
	defer cancel()
	response, err := d.inbox.ObserveTarget(rpcCtx, runtimeRequest(session.Token, &inboxv1.ObserveTargetRequest{Target: run.GetTarget(), Limit: 200}))
	d.invalidateRuntimeOnUnauthenticated(ctx, session, err)
	if err != nil {
		return triggerContext{}, fmt.Errorf("observe run target: %w", err)
	}
	if response == nil {
		return triggerContext{}, errors.New("observe run target returned no response")
	}
	return authoritativeTrigger(run, response.Msg)
}

func authoritativeTrigger(run *runv1.Run, observed *inboxv1.ObserveTargetResponse) (triggerContext, error) {
	if run == nil || !validUUID(run.GetTriggerMessageId()) || !validUUID(run.GetSpaceId()) ||
		run.GetTriggerTargetSequence() == 0 || run.GetTarget() == nil {
		return triggerContext{}, errors.New("run trigger facts are invalid")
	}
	if _, err := messagecodec.ParseTarget(run.GetTarget()); err != nil {
		return triggerContext{}, fmt.Errorf("parse run target: %w", err)
	}
	if observed == nil || !proto.Equal(observed.GetTarget(), run.GetTarget()) || observed.GetHeadSequence() < run.GetTriggerTargetSequence() {
		return triggerContext{}, errors.New("observed target does not contain the run trigger")
	}
	var trigger *spacev1.Message
	for _, message := range observed.GetMessages() {
		if message == nil || message.GetId() != run.GetTriggerMessageId() {
			continue
		}
		if trigger != nil {
			return triggerContext{}, errors.New("observed target contains duplicate trigger messages")
		}
		trigger = message
	}
	if trigger == nil || trigger.GetSpaceId() != run.GetSpaceId() || trigger.GetTargetSequence() != run.GetTriggerTargetSequence() ||
		!messageMatchesTarget(trigger, run.GetTarget()) || strings.TrimSpace(trigger.GetBody()) == "" {
		return triggerContext{}, errors.New("observed trigger message does not match run facts")
	}
	if err := messagecodec.ValidateBody(trigger.GetBody()); err != nil {
		return triggerContext{}, fmt.Errorf("validate trigger body: %w", err)
	}
	messages := make([]computerruntime.ContextMessage, 0, len(observed.GetMessages()))
	for _, message := range observed.GetMessages() {
		if message == nil || message.GetAuthor() == nil || strings.TrimSpace(message.GetBody()) == "" {
			return triggerContext{}, errors.New("observed target contains an invalid context message")
		}
		messages = append(messages, computerruntime.ContextMessage{
			ID: message.GetId(), TargetSequence: message.GetTargetSequence(), AuthorKind: message.GetAuthor().GetKind().String(),
			AuthorID: message.GetAuthor().GetId(), Body: message.GetBody(),
		})
	}
	return triggerContext{
		spaceID: run.GetSpaceId(), threadRootMessageID: trigger.GetThreadRootMessageId(),
		observedHead: observed.GetHeadSequence(), messages: messages, body: trigger.GetBody(),
	}, nil
}

func messageMatchesTarget(message *spacev1.Message, target *spacev1.MessageTarget) bool {
	if message == nil || target == nil {
		return false
	}
	if spaceID := target.GetSpaceId(); spaceID != "" {
		return message.GetThreadRootMessageId() == "" && message.GetSpaceId() == spaceID
	}
	threadID := target.GetThreadRootMessageId()
	return threadID != "" && message.GetThreadRootMessageId() == threadID
}

func (d *Daemon) claimRun(ctx context.Context, session computerstate.RuntimeSession, runID string) (*runv1.Run, error) {
	payloadHash := mutationHash(string(computerstate.MutationRunClaim), runID, session.ComputerID, fmt.Sprint(session.PlacementDesiredRevision))
	attempt, err := d.config.State.BeginMutation(ctx, computerstate.MutationAttempt{
		Operation: computerstate.MutationRunClaim, SubjectID: runID, PayloadHash: payloadHash,
		RunID: runID, CreatedAt: d.config.Now(),
	})
	if err != nil {
		return nil, fmt.Errorf("begin run claim: %w", err)
	}
	rpcCtx, cancel := d.rpcContext(ctx)
	response, err := d.runs.ClaimRun(rpcCtx, runtimeRequest(session.Token, &runv1.ClaimRunRequest{RequestId: attempt.RequestID, RunId: runID}))
	cancel()
	if err != nil {
		d.invalidateRuntimeOnUnauthenticated(ctx, session, err)
		d.finishCanonicalMutation(ctx, attempt.RequestID, err)
		return nil, fmt.Errorf("claim run: %w", err)
	}
	if response == nil || response.Msg.GetRun() == nil {
		return nil, errors.New("claim run returned no response")
	}
	run := response.Msg.GetRun()
	if err := validateRunningRun(run, session); err != nil {
		return nil, err
	}
	expiresAt := run.GetLeaseExpiresAt().AsTime()
	if err := d.config.State.CompleteMutation(ctx, attempt.RequestID, computerstate.MutationSucceeded, run.GetAttempt(), run.GetFence(), &expiresAt, d.config.Now()); err != nil {
		return nil, fmt.Errorf("complete run claim: %w", err)
	}
	return run, nil
}

func (d *Daemon) reconcileClaimMutation(ctx context.Context, session computerstate.RuntimeSession, run *runv1.Run) error {
	payloadHash := mutationHash(string(computerstate.MutationRunClaim), run.GetId(), session.ComputerID, fmt.Sprint(session.PlacementDesiredRevision))
	expiresAt := run.GetLeaseExpiresAt().AsTime()
	return d.reconcilePendingMutation(ctx, computerstate.MutationRunClaim, run.GetId(), payloadHash, run.GetAttempt(), run.GetFence(), &expiresAt)
}

func (d *Daemon) reconcilePendingMutation(ctx context.Context, operation computerstate.MutationOperation, subjectID string, payloadHash [sha256.Size]byte, responseAttempt, responseFence uint64, responseExpiresAt *time.Time) error {
	attempts, err := d.config.State.MutationAttempts(ctx, operation, subjectID)
	if err != nil {
		return fmt.Errorf("list %s mutation attempts: %w", operation, err)
	}
	for _, attempt := range attempts {
		if attempt.Status != computerstate.MutationPending {
			continue
		}
		if attempt.PayloadHash != payloadHash {
			return fmt.Errorf("pending %s mutation payload conflicts with Server facts", operation)
		}
		if err := d.config.State.CompleteMutation(ctx, attempt.RequestID, computerstate.MutationSucceeded, responseAttempt, responseFence, responseExpiresAt, d.config.Now()); err != nil {
			return fmt.Errorf("complete pending %s mutation: %w", operation, err)
		}
		return nil
	}
	return nil
}
