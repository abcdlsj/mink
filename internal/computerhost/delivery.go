package computerhost

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	deliveryv1 "github.com/abcdlsj/sumi/gen/go/sumi/delivery/v1"
	inboxv1 "github.com/abcdlsj/sumi/gen/go/sumi/inbox/v1"
	spacev1 "github.com/abcdlsj/sumi/gen/go/sumi/space/v1"
	"github.com/abcdlsj/sumi/internal/agentmessage"
	"github.com/abcdlsj/sumi/internal/computerstate"
	"google.golang.org/protobuf/proto"
)

func (d *Daemon) deliveryLoop(ctx context.Context, identity computerstate.Identity) {
	d.periodicLoop(ctx, d.config.DeliveryInterval, func(ctx context.Context) error {
		return d.dispatchDeliveries(ctx, identity)
	})
}

func (d *Daemon) dispatchDeliveries(ctx context.Context, identity computerstate.Identity) error {
	if d.config.Executor == nil {
		return nil
	}
	sessions, err := d.config.State.RuntimeSessions(ctx)
	if err != nil {
		return fmt.Errorf("list runtime sessions for delivery dispatch: %w", err)
	}
	var dispatchErrors []error
	for _, session := range sessions {
		if session.ComputerID != identity.ComputerID || !session.ExpiresAt.After(d.config.Now()) {
			continue
		}
		if err := d.dispatchAgent(ctx, identity, session); err != nil {
			dispatchErrors = append(dispatchErrors, fmt.Errorf("dispatch agent %q: %w", session.AgentID, err))
		}
	}
	return errors.Join(dispatchErrors...)
}

func (d *Daemon) dispatchAgent(ctx context.Context, identity computerstate.Identity, session computerstate.RuntimeSession) error {
	if executor, ok := d.config.Executor.(EligibleExecutor); ok {
		eligible, err := executor.Eligible(ctx, session.AgentID)
		if err != nil {
			return fmt.Errorf("check executor eligibility: %w", err)
		}
		if !eligible {
			return nil
		}
	}
	rpcCtx, cancel := d.rpcContext(ctx)
	response, err := d.deliveries.ListDeliveries(rpcCtx, runtimeRequest(session.Token, &deliveryv1.ListDeliveriesRequest{Limit: 50}))
	cancel()
	if err != nil {
		d.invalidateRuntimeOnUnauthenticated(ctx, session, err)
		return fmt.Errorf("list deliveries: %w", err)
	}
	if response == nil {
		return errors.New("list deliveries returned no response")
	}
	run := response.Msg.GetActiveRun()
	delivery := response.Msg.GetActiveDelivery()
	launch := response.Msg.GetActiveLaunch()
	if err := validateActiveDeliveryResponse(session.AgentID, delivery, run, launch); err != nil {
		return err
	}
	if run != nil {
		if err := d.reconcileActiveMutations(ctx, session, delivery, run, launch); err != nil {
			return err
		}
	}
	var trigger triggerContext
	observedBeforeAccept := false
	if run == nil && len(response.Msg.GetDeliveries()) > 0 {
		delivery = response.Msg.GetDeliveries()[0]
		if err := validateAvailableDelivery(delivery, session.AgentID); err != nil {
			return err
		}
		trigger, err = d.observeDelivery(ctx, session, delivery)
		if err != nil {
			return err
		}
		run, err = d.acceptDelivery(ctx, session, delivery)
		if err != nil {
			return err
		}
		launch = nil
		observedBeforeAccept = true
	}
	if run == nil || delivery == nil {
		return nil
	}
	if !observedBeforeAccept {
		trigger, err = d.observeDelivery(ctx, session, delivery)
		if err != nil {
			return err
		}
	}
	if run.GetBasisTargetSequence() == 0 || (observedBeforeAccept && run.GetBasisTargetSequence() != trigger.observedHead) {
		return errors.New("run basis target sequence is invalid")
	}
	if launch == nil || !launch.GetExpiresAt().AsTime().After(d.config.Now()) {
		launch, err = d.claimRun(ctx, session, run.GetId())
		if err != nil {
			return err
		}
	}
	if launch == nil {
		return nil
	}
	if err := validateLaunch(launch, run.GetId(), session.AgentID, identity.ComputerID, session.PlacementGeneration); err != nil {
		return err
	}
	if !launch.GetExpiresAt().AsTime().After(d.config.Now()) {
		return nil
	}
	d.startWorker(ctx, session, delivery, run, launch, trigger)
	return nil
}

func (d *Daemon) observeDelivery(ctx context.Context, session computerstate.RuntimeSession, delivery *deliveryv1.Delivery) (triggerContext, error) {
	if delivery == nil || delivery.GetTarget() == nil {
		return triggerContext{}, errors.New("delivery target is required")
	}
	rpcCtx, cancel := d.rpcContext(ctx)
	defer cancel()
	response, err := d.inbox.ObserveTarget(rpcCtx, runtimeRequest(session.Token, &inboxv1.ObserveTargetRequest{
		Target: delivery.GetTarget(), Limit: 200,
	}))
	d.invalidateRuntimeOnUnauthenticated(ctx, session, err)
	if err != nil {
		return triggerContext{}, fmt.Errorf("observe delivery target: %w", err)
	}
	if response == nil {
		return triggerContext{}, errors.New("observe delivery target returned no response")
	}
	return authoritativeTrigger(delivery, response.Msg)
}

func authoritativeTrigger(delivery *deliveryv1.Delivery, observed *inboxv1.ObserveTargetResponse) (triggerContext, error) {
	if delivery == nil || !validUUID(delivery.GetTriggerMessageId()) || !validUUID(delivery.GetSpaceId()) ||
		delivery.GetTriggerTargetSequence() == 0 || delivery.GetTarget() == nil {
		return triggerContext{}, errors.New("delivery trigger facts are invalid")
	}
	if _, err := agentmessage.ParseTarget(delivery.GetTarget()); err != nil {
		return triggerContext{}, fmt.Errorf("parse delivery target: %w", err)
	}
	if observed == nil || !proto.Equal(observed.GetTarget(), delivery.GetTarget()) || observed.GetHeadSequence() < delivery.GetTriggerTargetSequence() {
		return triggerContext{}, errors.New("observed target does not contain the delivery trigger")
	}
	var trigger *spacev1.Message
	for _, message := range observed.GetMessages() {
		if message == nil || message.GetId() != delivery.GetTriggerMessageId() {
			continue
		}
		if trigger != nil {
			return triggerContext{}, errors.New("observed target contains duplicate trigger messages")
		}
		trigger = message
	}
	if trigger == nil || trigger.GetSpaceId() != delivery.GetSpaceId() || trigger.GetTargetSequence() != delivery.GetTriggerTargetSequence() ||
		!messageMatchesTarget(trigger, delivery.GetTarget()) || strings.TrimSpace(trigger.GetBody()) == "" {
		return triggerContext{}, errors.New("observed trigger message does not match delivery facts")
	}
	if err := agentmessage.ValidateBody(trigger.GetBody()); err != nil {
		return triggerContext{}, fmt.Errorf("validate trigger body: %w", err)
	}
	return triggerContext{
		spaceID: delivery.GetSpaceId(), threadRootMessageID: trigger.GetThreadRootMessageId(),
		observedHead: observed.GetHeadSequence(), body: trigger.GetBody(),
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

func (d *Daemon) acceptDelivery(ctx context.Context, session computerstate.RuntimeSession, delivery *deliveryv1.Delivery) (*deliveryv1.Run, error) {
	payloadHash := mutationHash(string(computerstate.MutationDeliveryAccept), delivery.GetId())
	attempt, err := d.config.State.BeginMutation(ctx, computerstate.MutationAttempt{
		Operation: computerstate.MutationDeliveryAccept, SubjectID: delivery.GetId(), PayloadHash: payloadHash,
		CreatedAt: d.config.Now(),
	})
	if err != nil {
		return nil, fmt.Errorf("begin delivery acceptance: %w", err)
	}
	rpcCtx, cancel := d.rpcContext(ctx)
	response, err := d.deliveries.AcceptDelivery(rpcCtx, runtimeRequest(session.Token, &deliveryv1.AcceptDeliveryRequest{
		RequestId: attempt.RequestID, DeliveryId: delivery.GetId(),
	}))
	cancel()
	if err != nil {
		d.invalidateRuntimeOnUnauthenticated(ctx, session, err)
		d.finishCanonicalMutation(ctx, attempt.RequestID, err)
		return nil, fmt.Errorf("accept delivery: %w", err)
	}
	if response == nil {
		return nil, errors.New("accept delivery returned no response")
	}
	run := response.Msg.GetRun()
	if err := validateRun(run, delivery.GetId(), session.AgentID); err != nil {
		return nil, err
	}
	if err := d.config.State.CompleteMutation(ctx, attempt.RequestID, computerstate.MutationSucceeded, "", 0, nil, d.config.Now()); err != nil {
		return nil, fmt.Errorf("complete delivery acceptance: %w", err)
	}
	return run, nil
}

func (d *Daemon) claimRun(ctx context.Context, session computerstate.RuntimeSession, runID string) (*deliveryv1.RunLaunch, error) {
	payloadHash := mutationHash(string(computerstate.MutationRunClaim), runID, session.ComputerID, fmt.Sprint(session.PlacementGeneration))
	attempt, err := d.config.State.BeginMutation(ctx, computerstate.MutationAttempt{
		Operation: computerstate.MutationRunClaim, SubjectID: claimSubject(runID, session), PayloadHash: payloadHash, RunID: runID,
		CreatedAt: d.config.Now(),
	})
	if err != nil {
		return nil, fmt.Errorf("begin run claim: %w", err)
	}
	rpcCtx, cancel := d.rpcContext(ctx)
	response, err := d.deliveries.ClaimRun(rpcCtx, runtimeRequest(session.Token, &deliveryv1.ClaimRunRequest{
		RequestId: attempt.RequestID, RunId: runID,
	}))
	cancel()
	if err != nil {
		d.invalidateRuntimeOnUnauthenticated(ctx, session, err)
		d.finishCanonicalMutation(ctx, attempt.RequestID, err)
		return nil, fmt.Errorf("claim run: %w", err)
	}
	if response == nil {
		return nil, errors.New("claim run returned no response")
	}
	launch := response.Msg.GetLaunch()
	if err := validateLaunch(launch, runID, session.AgentID, session.ComputerID, session.PlacementGeneration); err != nil {
		return nil, err
	}
	expiresAt := launch.GetExpiresAt().AsTime()
	if err := d.config.State.CompleteMutation(ctx, attempt.RequestID, computerstate.MutationSucceeded, launch.GetId(), launch.GetFence(), &expiresAt, d.config.Now()); err != nil {
		return nil, fmt.Errorf("complete run claim: %w", err)
	}
	return launch, nil
}

func (d *Daemon) reconcileActiveMutations(ctx context.Context, session computerstate.RuntimeSession, delivery *deliveryv1.Delivery, run *deliveryv1.Run, launch *deliveryv1.RunLaunch) error {
	acceptHash := mutationHash(string(computerstate.MutationDeliveryAccept), delivery.GetId())
	if err := d.reconcilePendingMutation(ctx, computerstate.MutationDeliveryAccept, delivery.GetId(), acceptHash, "", 0, nil); err != nil {
		return err
	}
	if launch == nil || launch.GetHolderComputerId() != session.ComputerID || launch.GetHolderPlacementGeneration() != session.PlacementGeneration {
		return nil
	}
	expiresAt := launch.GetExpiresAt().AsTime()
	d.updateWorkerLease(run.GetId(), launch.GetId(), launch.GetFence(), expiresAt)
	claimHash := mutationHash(string(computerstate.MutationRunClaim), run.GetId(), session.ComputerID, fmt.Sprint(session.PlacementGeneration))
	if err := d.reconcilePendingMutation(ctx, computerstate.MutationRunClaim, claimSubject(run.GetId(), session), claimHash, launch.GetId(), launch.GetFence(), &expiresAt); err != nil {
		return err
	}
	renewHash := mutationHash(string(computerstate.MutationRunRenew), run.GetId(), launch.GetId(), fmt.Sprint(launch.GetFence()), session.ComputerID, fmt.Sprint(session.PlacementGeneration))
	return d.reconcilePendingMutation(ctx, computerstate.MutationRunRenew, launch.GetId(), renewHash, launch.GetId(), launch.GetFence(), &expiresAt)
}

func (d *Daemon) updateWorkerLease(runID, launchID string, fence uint64, expiresAt time.Time) {
	if runID == "" || launchID == "" || fence == 0 || expiresAt.IsZero() {
		return
	}
	d.workersMu.Lock()
	defer d.workersMu.Unlock()
	worker, found := d.workers[runID]
	if !found || worker.launchID != launchID || worker.fence != fence || worker.leaseExpiry == nil {
		return
	}
	newExpiry := expiresAt.UnixNano()
	for {
		current := worker.leaseExpiry.Load()
		if newExpiry <= current || worker.leaseExpiry.CompareAndSwap(current, newExpiry) {
			return
		}
	}
}

func claimSubject(runID string, session computerstate.RuntimeSession) string {
	return fmt.Sprintf("%s:%s:%d", runID, session.ComputerID, session.PlacementGeneration)
}

func (d *Daemon) reconcilePendingMutation(ctx context.Context, operation computerstate.MutationOperation, subjectID string, payloadHash [sha256.Size]byte, responseLaunchID string, responseFence uint64, responseExpiresAt *time.Time) error {
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
		if err := d.config.State.CompleteMutation(ctx, attempt.RequestID, computerstate.MutationSucceeded, responseLaunchID, responseFence, responseExpiresAt, d.config.Now()); err != nil {
			return fmt.Errorf("complete pending %s mutation: %w", operation, err)
		}
		return nil
	}
	return nil
}
