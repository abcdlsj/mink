package computerhost

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"

	"connectrpc.com/connect"
	computerv1 "github.com/abcdlsj/sumi/gen/go/sumi/computer/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/computer/v1/computerv1connect"
	deliveryv1 "github.com/abcdlsj/sumi/gen/go/sumi/delivery/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/delivery/v1/deliveryv1connect"
	inboxv1 "github.com/abcdlsj/sumi/gen/go/sumi/inbox/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/inbox/v1/inboxv1connect"
	placementv1 "github.com/abcdlsj/sumi/gen/go/sumi/placement/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/placement/v1/placementv1connect"
	runtimev1 "github.com/abcdlsj/sumi/gen/go/sumi/runtime/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/runtime/v1/runtimev1connect"
	"github.com/abcdlsj/sumi/internal/computerstate"
	"github.com/abcdlsj/sumi/internal/workspace"
	"github.com/google/uuid"
)

type Executor interface {
	Execute(context.Context, Execution) (Completion, error)
}

type Execution struct {
	AgentID             string
	DeliveryID          string
	RunID               string
	LaunchID            string
	Fence               uint64
	PlacementGeneration uint64
	Workspace           string
}

type Completion struct {
	Outcome           deliveryv1.RunOutcome
	Body              string
	MentionedAgentIDs []string
}

type DaemonConfig struct {
	ServerURL          string
	DataRoot           string
	State              *computerstate.State
	HTTPClient         *http.Client
	Executor           Executor
	HeartbeatInterval  time.Duration
	SnapshotInterval   time.Duration
	DeliveryInterval   time.Duration
	RunRenewInterval   time.Duration
	OutboxInterval     time.Duration
	RuntimeRenewBefore time.Duration
	RPCDeadline        time.Duration
	BackoffMax         time.Duration
	Now                func() time.Time
	RetryJitter        func(time.Duration) time.Duration
}

type computerDaemonClient interface {
	HeartbeatComputer(context.Context, *connect.Request[computerv1.HeartbeatComputerRequest]) (*connect.Response[computerv1.HeartbeatComputerResponse], error)
}

type placementDaemonClient interface {
	ListComputerPlacements(context.Context, *connect.Request[placementv1.ListComputerPlacementsRequest]) (*connect.Response[placementv1.ListComputerPlacementsResponse], error)
	AcknowledgeAgentPlacement(context.Context, *connect.Request[placementv1.AcknowledgeAgentPlacementRequest]) (*connect.Response[placementv1.AcknowledgeAgentPlacementResponse], error)
}

type runtimeDaemonClient interface {
	CreateAgentRuntimeSession(context.Context, *connect.Request[runtimev1.CreateAgentRuntimeSessionRequest]) (*connect.Response[runtimev1.CreateAgentRuntimeSessionResponse], error)
	RenewAgentRuntimeSession(context.Context, *connect.Request[runtimev1.RenewAgentRuntimeSessionRequest]) (*connect.Response[runtimev1.RenewAgentRuntimeSessionResponse], error)
}

type inboxDaemonClient interface {
	ObserveTarget(context.Context, *connect.Request[inboxv1.ObserveTargetRequest]) (*connect.Response[inboxv1.ObserveTargetResponse], error)
}

type deliveryDaemonClient interface {
	ListDeliveries(context.Context, *connect.Request[deliveryv1.ListDeliveriesRequest]) (*connect.Response[deliveryv1.ListDeliveriesResponse], error)
	AcceptDelivery(context.Context, *connect.Request[deliveryv1.AcceptDeliveryRequest]) (*connect.Response[deliveryv1.AcceptDeliveryResponse], error)
	ClaimRun(context.Context, *connect.Request[deliveryv1.ClaimRunRequest]) (*connect.Response[deliveryv1.ClaimRunResponse], error)
	RenewRun(context.Context, *connect.Request[deliveryv1.RenewRunRequest]) (*connect.Response[deliveryv1.RenewRunResponse], error)
	CompleteRun(context.Context, *connect.Request[deliveryv1.CompleteRunRequest]) (*connect.Response[deliveryv1.CompleteRunResponse], error)
}

type runWorker struct {
	cancel     context.CancelFunc
	agentID    string
	generation uint64
	launchID   string
	fence      uint64
}

type Daemon struct {
	config     DaemonConfig
	computers  computerDaemonClient
	placements placementDaemonClient
	runtimes   runtimeDaemonClient
	inbox      inboxDaemonClient
	deliveries deliveryDaemonClient

	workersMu sync.Mutex
	workers   map[string]runWorker
	workersWG sync.WaitGroup

	persistOutbox func(context.Context, computerstate.OutboxEvent) error
}

type retryGate struct {
	failures uint
	next     time.Time
}

func NewDaemon(config DaemonConfig) *Daemon {
	client := config.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	setDaemonDefaults(&config)
	return &Daemon{
		config:     config,
		computers:  computerv1connect.NewComputerServiceClient(client, config.ServerURL),
		placements: placementv1connect.NewPlacementServiceClient(client, config.ServerURL),
		runtimes:   runtimev1connect.NewAgentRuntimeServiceClient(client, config.ServerURL),
		inbox:      inboxv1connect.NewInboxServiceClient(client, config.ServerURL),
		deliveries: deliveryv1connect.NewDeliveryServiceClient(client, config.ServerURL),
		workers:    make(map[string]runWorker),
	}
}

func (d *Daemon) Run(ctx context.Context) error {
	if d.config.State == nil {
		return errors.New("computer state is required")
	}
	identity, found, err := d.config.State.Identity(ctx)
	if err != nil {
		return err
	}
	if !found || identity.ServerURL != d.config.ServerURL {
		return errors.New("computer identity is unavailable for this Server")
	}
	var loops sync.WaitGroup
	loops.Add(3)
	go func() {
		defer loops.Done()
		d.connectivitySupervisor(ctx, identity)
	}()
	go func() {
		defer loops.Done()
		d.deliveryLoop(ctx, identity)
	}()
	go func() {
		defer loops.Done()
		d.outboxLoop(ctx)
	}()
	<-ctx.Done()
	d.stopAllWorkers()
	loops.Wait()
	d.workersWG.Wait()
	return nil
}

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
	d.periodicLoop(ctx, d.config.HeartbeatInterval, func(ctx context.Context) bool {
		return d.heartbeat(ctx, identity)
	})
}

func (d *Daemon) snapshotLoop(ctx context.Context, identity computerstate.Identity) {
	d.periodicLoop(ctx, d.config.SnapshotInterval, func(ctx context.Context) bool {
		return d.syncPlacements(ctx, identity)
	})
}

func (d *Daemon) periodicLoop(ctx context.Context, interval time.Duration, operation func(context.Context) bool) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var retry retryGate
	retry.record(d.config.Now(), operation(ctx), interval, d.config.BackoffMax, d.config.RetryJitter)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if retry.ready(d.config.Now()) {
				retry.record(d.config.Now(), operation(ctx), interval, d.config.BackoffMax, d.config.RetryJitter)
			}
		}
	}
}

func (d *Daemon) heartbeat(ctx context.Context, identity computerstate.Identity) bool {
	rpcCtx, cancel := d.rpcContext(ctx)
	defer cancel()
	_, err := d.computers.HeartbeatComputer(rpcCtx, connect.NewRequest(&computerv1.HeartbeatComputerRequest{
		ComputerId: identity.ComputerID, RegistrationKey: identity.RegistrationKey,
	}))
	return err == nil
}

func (d *Daemon) syncPlacements(ctx context.Context, identity computerstate.Identity) bool {
	rpcCtx, cancel := d.rpcContext(ctx)
	response, err := d.placements.ListComputerPlacements(rpcCtx, connect.NewRequest(&placementv1.ListComputerPlacementsRequest{
		ComputerId: identity.ComputerID, RegistrationKey: identity.RegistrationKey,
	}))
	cancel()
	if err != nil {
		return false
	}
	if response == nil {
		return false
	}
	placements := response.Msg.GetPlacements()
	seen := make(map[string]struct{}, len(placements))
	for _, placement := range placements {
		if !validPlacement(placement, identity.ComputerID) {
			return false
		}
		if _, duplicate := seen[placement.GetAgentId()]; duplicate {
			return false
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
	succeeded := true
	for _, placement := range placements {
		switch placement.GetState() {
		case placementv1.PlacementState_PLACEMENT_STATE_PENDING:
			placement = d.provisionPlacement(ctx, identity, placement)
		case placementv1.PlacementState_PLACEMENT_STATE_ACTIVE:
		case placementv1.PlacementState_PLACEMENT_STATE_FAILED:
		}
		if placement == nil {
			succeeded = false
			continue
		}
		if !validPlacement(placement, identity.ComputerID) {
			succeeded = false
			continue
		}
		if placement != nil && placement.GetState() == placementv1.PlacementState_PLACEMENT_STATE_ACTIVE {
			active[placement.GetAgentId()] = placement.GetGeneration()
			if !d.ensureRuntime(ctx, identity, placement.GetAgentId(), placement.GetGeneration()) {
				succeeded = false
			}
		}
	}
	sessions, err := d.config.State.RuntimeSessions(ctx)
	if err != nil {
		return false
	}
	for _, session := range sessions {
		generation, current := active[session.AgentID]
		if current && generation == session.PlacementGeneration && session.ComputerID == identity.ComputerID {
			continue
		}
		_ = d.config.State.DeleteRuntimeSession(ctx, session.AgentID, session.ComputerID, session.PlacementGeneration)
		d.stopWorkerBinding(session.AgentID, session.PlacementGeneration)
	}
	return succeeded
}

func validPlacement(placement *placementv1.AgentPlacement, computerID string) bool {
	if placement == nil || placement.GetComputerId() != computerID || placement.GetGeneration() == 0 {
		return false
	}
	if _, err := uuid.Parse(placement.GetAgentId()); err != nil {
		return false
	}
	return placement.GetState() == placementv1.PlacementState_PLACEMENT_STATE_PENDING ||
		placement.GetState() == placementv1.PlacementState_PLACEMENT_STATE_ACTIVE ||
		placement.GetState() == placementv1.PlacementState_PLACEMENT_STATE_FAILED
}

func (d *Daemon) provisionPlacement(ctx context.Context, identity computerstate.Identity, placement *placementv1.AgentPlacement) *placementv1.AgentPlacement {
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
		return nil
	}
	return response.Msg.GetPlacement()
}

func (d *Daemon) ensureRuntime(ctx context.Context, identity computerstate.Identity, agentID string, generation uint64) bool {
	now := d.config.Now()
	session, found, err := d.config.State.RuntimeSession(ctx, agentID)
	if err != nil {
		return false
	}
	if found && session.ComputerID == identity.ComputerID && session.PlacementGeneration == generation && session.ExpiresAt.After(now.Add(d.config.RuntimeRenewBefore)) {
		return true
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
			return false
		}
		if connect.CodeOf(renewErr) != connect.CodeUnauthenticated {
			return false
		}
		_ = d.config.State.DeleteRuntimeSession(ctx, agentID, identity.ComputerID, generation)
	}
	rpcCtx, cancel := d.rpcContext(ctx)
	response, err := d.runtimes.CreateAgentRuntimeSession(rpcCtx, connect.NewRequest(&runtimev1.CreateAgentRuntimeSessionRequest{
		ComputerId: identity.ComputerID, RegistrationKey: identity.RegistrationKey,
		AgentId: agentID, PlacementGeneration: generation,
	}))
	cancel()
	if err != nil {
		return false
	}
	if response == nil {
		return false
	}
	return d.saveRuntimeResponse(ctx, response.Msg.GetSession(), agentID, identity.ComputerID, generation, now)
}

func (d *Daemon) saveRuntimeResponse(ctx context.Context, session *runtimev1.AgentRuntimeSession, agentID, computerID string, generation uint64, updatedAt time.Time) bool {
	if session == nil || session.GetAgentId() != agentID || session.GetComputerId() != computerID ||
		session.GetPlacementGeneration() != generation || session.GetExpiresAt() == nil ||
		session.GetExpiresAt().CheckValid() != nil || !session.GetExpiresAt().AsTime().After(updatedAt) ||
		!canonicalSecret(session.GetToken()) {
		return false
	}
	return d.config.State.SaveRuntimeSession(ctx, computerstate.RuntimeSession{
		AgentID: session.GetAgentId(), ComputerID: session.GetComputerId(),
		PlacementGeneration: session.GetPlacementGeneration(), Token: session.GetToken(),
		ExpiresAt: session.GetExpiresAt().AsTime(), UpdatedAt: updatedAt,
	}) == nil
}

func (d *Daemon) deliveryLoop(ctx context.Context, identity computerstate.Identity) {
	ticker := time.NewTicker(d.config.DeliveryInterval)
	defer ticker.Stop()
	var retry retryGate
	retry.record(d.config.Now(), d.dispatchDeliveries(ctx, identity), d.config.DeliveryInterval, d.config.BackoffMax, d.config.RetryJitter)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if retry.ready(d.config.Now()) {
				retry.record(d.config.Now(), d.dispatchDeliveries(ctx, identity), d.config.DeliveryInterval, d.config.BackoffMax, d.config.RetryJitter)
			}
		}
	}
}

func (d *Daemon) dispatchDeliveries(ctx context.Context, identity computerstate.Identity) bool {
	if d.config.Executor == nil {
		return true
	}
	sessions, err := d.config.State.RuntimeSessions(ctx)
	if err != nil {
		return false
	}
	succeeded := true
	for _, session := range sessions {
		if session.ComputerID != identity.ComputerID || !session.ExpiresAt.After(d.config.Now()) {
			continue
		}
		if !d.dispatchAgent(ctx, identity, session) {
			succeeded = false
		}
	}
	return succeeded
}

func (d *Daemon) dispatchAgent(ctx context.Context, identity computerstate.Identity, session computerstate.RuntimeSession) bool {
	rpcCtx, cancel := d.rpcContext(ctx)
	response, err := d.deliveries.ListDeliveries(rpcCtx, runtimeRequest(session.Token, &deliveryv1.ListDeliveriesRequest{Limit: 50}))
	cancel()
	if err != nil {
		d.invalidateRuntimeOnUnauthenticated(ctx, session, err)
		return false
	}
	if response == nil {
		return false
	}
	run := response.Msg.GetActiveRun()
	delivery := response.Msg.GetActiveDelivery()
	launch := response.Msg.GetActiveLaunch()
	if !validActiveDeliveryResponse(session.AgentID, delivery, run, launch) {
		return false
	}
	if run == nil && len(response.Msg.GetDeliveries()) > 0 {
		delivery = response.Msg.GetDeliveries()[0]
		if !validAvailableDelivery(delivery, session.AgentID) {
			return false
		}
		if !d.observeDelivery(ctx, session, delivery) {
			return false
		}
		run = d.acceptDelivery(ctx, session, delivery)
		launch = nil
	}
	if run == nil || delivery == nil {
		return true
	}
	if launch == nil || !launch.GetExpiresAt().AsTime().After(d.config.Now()) {
		launch = d.claimRun(ctx, session, run.GetId())
	}
	if launch == nil || !validLaunch(launch, run.GetId(), session.AgentID, identity.ComputerID, session.PlacementGeneration) ||
		!launch.GetExpiresAt().AsTime().After(d.config.Now()) {
		return true
	}
	d.startWorker(ctx, session, delivery, run, launch)
	return true
}

func (d *Daemon) observeDelivery(ctx context.Context, session computerstate.RuntimeSession, delivery *deliveryv1.Delivery) bool {
	if delivery == nil || delivery.GetTarget() == nil {
		return false
	}
	rpcCtx, cancel := d.rpcContext(ctx)
	defer cancel()
	_, err := d.inbox.ObserveTarget(rpcCtx, runtimeRequest(session.Token, &inboxv1.ObserveTargetRequest{
		Target: delivery.GetTarget(), Limit: 200,
	}))
	d.invalidateRuntimeOnUnauthenticated(ctx, session, err)
	return err == nil
}

func (d *Daemon) acceptDelivery(ctx context.Context, session computerstate.RuntimeSession, delivery *deliveryv1.Delivery) *deliveryv1.Run {
	payloadHash := mutationHash("delivery.accept", delivery.GetId())
	attempt, err := d.config.State.BeginMutation(ctx, computerstate.MutationAttempt{
		Operation: "delivery.accept", SubjectID: delivery.GetId(), PayloadHash: payloadHash,
		CreatedAt: d.config.Now(),
	})
	if err != nil {
		return nil
	}
	rpcCtx, cancel := d.rpcContext(ctx)
	response, err := d.deliveries.AcceptDelivery(rpcCtx, runtimeRequest(session.Token, &deliveryv1.AcceptDeliveryRequest{
		RequestId: attempt.RequestID, DeliveryId: delivery.GetId(),
	}))
	cancel()
	if err != nil {
		d.invalidateRuntimeOnUnauthenticated(ctx, session, err)
		d.finishCanonicalMutation(ctx, attempt.RequestID, err)
		return nil
	}
	if response == nil {
		return nil
	}
	run := response.Msg.GetRun()
	if !validRun(run, delivery.GetId(), session.AgentID) {
		return nil
	}
	if err := d.config.State.CompleteMutation(ctx, attempt.RequestID, "succeeded", "", 0, nil, d.config.Now()); err != nil {
		return nil
	}
	return run
}

func (d *Daemon) claimRun(ctx context.Context, session computerstate.RuntimeSession, runID string) *deliveryv1.RunLaunch {
	payloadHash := mutationHash("run.claim", runID, session.ComputerID, fmt.Sprint(session.PlacementGeneration))
	attempt, err := d.config.State.BeginMutation(ctx, computerstate.MutationAttempt{
		Operation: "run.claim", SubjectID: runID, PayloadHash: payloadHash, RunID: runID,
		CreatedAt: d.config.Now(),
	})
	if err != nil {
		return nil
	}
	rpcCtx, cancel := d.rpcContext(ctx)
	response, err := d.deliveries.ClaimRun(rpcCtx, runtimeRequest(session.Token, &deliveryv1.ClaimRunRequest{
		RequestId: attempt.RequestID, RunId: runID,
	}))
	cancel()
	if err != nil {
		d.invalidateRuntimeOnUnauthenticated(ctx, session, err)
		d.finishCanonicalMutation(ctx, attempt.RequestID, err)
		return nil
	}
	if response == nil {
		return nil
	}
	launch := response.Msg.GetLaunch()
	if !validLaunch(launch, runID, session.AgentID, session.ComputerID, session.PlacementGeneration) {
		return nil
	}
	expiresAt := launch.GetExpiresAt().AsTime()
	if err := d.config.State.CompleteMutation(ctx, attempt.RequestID, "succeeded", launch.GetId(), launch.GetFence(), &expiresAt, d.config.Now()); err != nil {
		return nil
	}
	return launch
}

func (d *Daemon) startWorker(parent context.Context, session computerstate.RuntimeSession, delivery *deliveryv1.Delivery, run *deliveryv1.Run, launch *deliveryv1.RunLaunch) {
	events, err := d.config.State.Outbox(parent)
	if err != nil {
		return
	}
	for _, event := range events {
		if event.RunID == run.GetId() && event.LaunchID == launch.GetId() && event.Fence == launch.GetFence() {
			return
		}
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
	d.workers[run.GetId()] = runWorker{
		cancel: cancel, agentID: session.AgentID, generation: session.PlacementGeneration,
		launchID: launch.GetId(), fence: launch.GetFence(),
	}
	d.workersWG.Add(1)
	d.workersMu.Unlock()
	go func() {
		defer d.workersWG.Done()
		d.runLeaseWatchdog(ctx, session, delivery, run, launch)
	}()
}

func (d *Daemon) runLeaseWatchdog(ctx context.Context, session computerstate.RuntimeSession, delivery *deliveryv1.Delivery, run *deliveryv1.Run, launch *deliveryv1.RunLaunch) {
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
			AgentID: session.AgentID, DeliveryID: delivery.GetId(), RunID: run.GetId(),
			LaunchID: launch.GetId(), Fence: launch.GetFence(), PlacementGeneration: session.PlacementGeneration,
			Workspace: workspacePath,
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
			if ctx.Err() != nil || !expiresAt.After(d.config.Now()) || executionResult.err != nil || !validCompletion(executionResult.completion) {
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
			stored, current := d.tryEnqueueCompletion(ctx, expiresAt, run.GetId(), launch.GetId(), launch.GetFence(), event)
			if stored || !current {
				return
			}
			if !scheduleEnqueueRetry() {
				return
			}
		case <-enqueueRetry:
			expiry.Stop()
			enqueueRetry = nil
			stored, current := d.tryEnqueueCompletion(ctx, expiresAt, run.GetId(), launch.GetId(), launch.GetFence(), *pendingEvent)
			if stored || !current {
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
			updated, ok := d.renewRun(ctx, currentSession, run.GetId(), launch.GetId(), launch.GetFence(), expiresAt)
			if ok {
				expiresAt = updated
			}
		}
	}
}

func (d *Daemon) renewRun(ctx context.Context, session computerstate.RuntimeSession, runID, launchID string, fence uint64, previousExpiry time.Time) (time.Time, bool) {
	payloadHash := mutationHash("run.renew", runID, launchID, fmt.Sprint(fence), session.ComputerID, fmt.Sprint(session.PlacementGeneration))
	attempt, err := d.config.State.BeginMutation(ctx, computerstate.MutationAttempt{
		Operation: "run.renew", SubjectID: launchID, PayloadHash: payloadHash,
		RunID: runID, LaunchID: launchID, Fence: fence, CreatedAt: d.config.Now(),
	})
	if err != nil {
		return time.Time{}, false
	}
	rpcCtx, cancel := d.rpcContext(ctx)
	response, err := d.deliveries.RenewRun(rpcCtx, runtimeRequest(session.Token, &deliveryv1.RenewRunRequest{
		RequestId: attempt.RequestID, RunId: runID, LaunchId: launchID, Fence: fence,
	}))
	cancel()
	if err != nil {
		d.invalidateRuntimeOnUnauthenticated(ctx, session, err)
		d.finishCanonicalMutation(ctx, attempt.RequestID, err)
		return time.Time{}, false
	}
	if response == nil {
		return time.Time{}, false
	}
	updated := response.Msg.GetLaunch()
	if !validLaunch(updated, runID, session.AgentID, session.ComputerID, session.PlacementGeneration) ||
		updated.GetId() != launchID || updated.GetFence() != fence || !updated.GetExpiresAt().AsTime().After(previousExpiry) {
		return time.Time{}, false
	}
	expiresAt := updated.GetExpiresAt().AsTime()
	if err := d.config.State.CompleteMutation(ctx, attempt.RequestID, "succeeded", launchID, fence, &expiresAt, d.config.Now()); err != nil {
		return time.Time{}, false
	}
	return expiresAt, true
}

func (d *Daemon) outboxLoop(ctx context.Context) {
	ticker := time.NewTicker(d.config.OutboxInterval)
	defer ticker.Stop()
	var retry retryGate
	retry.record(d.config.Now(), d.dispatchOutbox(ctx), d.config.OutboxInterval, d.config.BackoffMax, d.config.RetryJitter)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if retry.ready(d.config.Now()) {
				retry.record(d.config.Now(), d.dispatchOutbox(ctx), d.config.OutboxInterval, d.config.BackoffMax, d.config.RetryJitter)
			}
		}
	}
}

func (d *Daemon) dispatchOutbox(ctx context.Context) bool {
	events, err := d.config.State.Outbox(ctx)
	if err != nil {
		return false
	}
	succeeded := true
	for _, event := range events {
		if event.State != "pending" {
			continue
		}
		session, found, err := d.config.State.RuntimeSession(ctx, event.AgentID)
		if err != nil || !found || !session.ExpiresAt.After(d.config.Now()) {
			continue
		}
		if err := d.config.State.RecordOutboxAttempt(ctx, event.OutboxEventID, d.config.Now()); err != nil {
			succeeded = false
			continue
		}
		rpcCtx, cancel := d.rpcContext(ctx)
		response, completeErr := d.deliveries.CompleteRun(rpcCtx, runtimeRequest(session.Token, &deliveryv1.CompleteRunRequest{
			RequestId: event.RequestID, OutboxEventId: event.OutboxEventID,
			RunId: event.RunID, LaunchId: event.LaunchID, Fence: event.Fence,
			Outcome: outcomeValue(event.Outcome), Body: event.Body, MentionedAgentIds: event.MentionedAgentIDs,
		}))
		cancel()
		if completeErr == nil && response != nil && validCompleteResponse(response.Msg, event) {
			if err := d.config.State.AckOutbox(ctx, event.OutboxEventID); err != nil {
				succeeded = false
			}
			continue
		}
		if completeErr == nil {
			succeeded = false
			continue
		}
		d.invalidateRuntimeOnUnauthenticated(ctx, session, completeErr)
		code := connect.CodeOf(completeErr)
		if code == connect.CodeFailedPrecondition || code == connect.CodeAlreadyExists {
			if err := d.config.State.TombstoneOutbox(ctx, event.OutboxEventID, code.String()); err != nil {
				succeeded = false
			}
		} else {
			succeeded = false
		}
	}
	return succeeded
}

func (d *Daemon) finishCanonicalMutation(ctx context.Context, requestID string, rpcErr error) {
	code := connect.CodeOf(rpcErr)
	if code == connect.CodeUnavailable || code == connect.CodeDeadlineExceeded || code == connect.CodeCanceled ||
		code == connect.CodeUnknown || code == connect.CodeUnauthenticated {
		return
	}
	_ = d.config.State.CompleteMutation(ctx, requestID, "failed", "", 0, nil, d.config.Now())
}

func (d *Daemon) rpcContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, d.config.RPCDeadline)
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

func (d *Daemon) tryEnqueueCompletion(ctx context.Context, expiresAt time.Time, runID, launchID string, fence uint64, event computerstate.OutboxEvent) (bool, bool) {
	d.workersMu.Lock()
	defer d.workersMu.Unlock()
	worker, found := d.workers[runID]
	if ctx.Err() != nil || !expiresAt.After(d.config.Now()) || !found || worker.launchID != launchID || worker.fence != fence {
		return false, false
	}
	persist := d.persistOutbox
	if persist == nil {
		persist = d.config.State.EnqueueOutbox
	}
	return persist(ctx, event) == nil, true
}

func (d *Daemon) invalidateRuntimeOnUnauthenticated(ctx context.Context, session computerstate.RuntimeSession, err error) {
	if connect.CodeOf(err) == connect.CodeUnauthenticated {
		_ = d.config.State.DeleteRuntimeSession(ctx, session.AgentID, session.ComputerID, session.PlacementGeneration)
	}
}

func validAvailableDelivery(delivery *deliveryv1.Delivery, agentID string) bool {
	return delivery != nil && validUUID(delivery.GetId()) && delivery.GetAgentId() == agentID &&
		delivery.GetTarget() != nil && delivery.GetState() == deliveryv1.DeliveryState_DELIVERY_STATE_AVAILABLE
}

func validActiveDeliveryResponse(agentID string, delivery *deliveryv1.Delivery, run *deliveryv1.Run, launch *deliveryv1.RunLaunch) bool {
	if run == nil {
		return delivery == nil && launch == nil
	}
	if delivery == nil || !validUUID(delivery.GetId()) || delivery.GetAgentId() != agentID || delivery.GetTarget() == nil ||
		delivery.GetState() != deliveryv1.DeliveryState_DELIVERY_STATE_ACCEPTED || !validRun(run, delivery.GetId(), agentID) {
		return false
	}
	return launch == nil || validLaunchFacts(launch, run.GetId(), agentID)
}

func validRun(run *deliveryv1.Run, deliveryID, agentID string) bool {
	return run != nil && validUUID(run.GetId()) && run.GetDeliveryId() == deliveryID && run.GetAgentId() == agentID &&
		(run.GetState() == deliveryv1.RunState_RUN_STATE_ACCEPTED || run.GetState() == deliveryv1.RunState_RUN_STATE_RUNNING)
}

func validLaunch(launch *deliveryv1.RunLaunch, runID, agentID, computerID string, generation uint64) bool {
	return validLaunchFacts(launch, runID, agentID) && launch.GetHolderComputerId() == computerID &&
		launch.GetHolderPlacementGeneration() == generation
}

func validLaunchFacts(launch *deliveryv1.RunLaunch, runID, agentID string) bool {
	return launch != nil && validUUID(launch.GetId()) && launch.GetRunId() == runID && launch.GetAgentId() == agentID &&
		validUUID(launch.GetHolderComputerId()) && launch.GetHolderPlacementGeneration() > 0 && launch.GetFence() > 0 &&
		launch.GetExpiresAt() != nil && launch.GetExpiresAt().CheckValid() == nil
}

func validCompleteResponse(response *deliveryv1.CompleteRunResponse, event computerstate.OutboxEvent) bool {
	if response == nil || response.GetCommittedAt() == nil || response.GetCommittedAt().CheckValid() != nil ||
		(response.GetMessage() == nil && response.GetHeldDraft() == nil) {
		return false
	}
	run := response.GetRun()
	if run == nil || run.GetId() != event.RunID || run.GetAgentId() != event.AgentID ||
		run.GetState() != deliveryv1.RunState_RUN_STATE_COMPLETED || run.GetOutcome() != outcomeValue(event.Outcome) ||
		run.GetCompletedAt() == nil || run.GetCompletedAt().CheckValid() != nil {
		return false
	}
	if message := response.GetMessage(); message != nil {
		return validUUID(message.GetId()) && run.GetResultMessageId() == message.GetId() && run.GetResultHeldDraftId() == ""
	}
	draft := response.GetHeldDraft()
	return validUUID(draft.GetId()) && run.GetResultHeldDraftId() == draft.GetId() && run.GetResultMessageId() == ""
}

func validUUID(value string) bool {
	_, err := uuid.Parse(value)
	return err == nil
}

func runtimeRequest[Request any](token string, message *Request) *connect.Request[Request] {
	request := connect.NewRequest(message)
	request.Header().Set("Authorization", "Bearer "+token)
	return request
}

func mutationHash(values ...string) [sha256.Size]byte {
	hash := sha256.New()
	for _, value := range values {
		hash.Write([]byte{0})
		hash.Write([]byte(value))
	}
	var result [sha256.Size]byte
	copy(result[:], hash.Sum(nil))
	return result
}

func validCompletion(completion Completion) bool {
	return completion.Outcome == deliveryv1.RunOutcome_RUN_OUTCOME_SUCCEEDED || completion.Outcome == deliveryv1.RunOutcome_RUN_OUTCOME_FAILED
}

func outcomeName(outcome deliveryv1.RunOutcome) string {
	if outcome == deliveryv1.RunOutcome_RUN_OUTCOME_SUCCEEDED {
		return "succeeded"
	}
	return "failed"
}

func outcomeValue(outcome string) deliveryv1.RunOutcome {
	if outcome == "succeeded" {
		return deliveryv1.RunOutcome_RUN_OUTCOME_SUCCEEDED
	}
	return deliveryv1.RunOutcome_RUN_OUTCOME_FAILED
}

func setDaemonDefaults(config *DaemonConfig) {
	if config.HeartbeatInterval <= 0 {
		config.HeartbeatInterval = 10 * time.Second
	}
	if config.SnapshotInterval <= 0 {
		config.SnapshotInterval = 10 * time.Second
	}
	if config.DeliveryInterval <= 0 {
		config.DeliveryInterval = time.Second
	}
	if config.RunRenewInterval <= 0 {
		config.RunRenewInterval = 20 * time.Second
	}
	if config.OutboxInterval <= 0 {
		config.OutboxInterval = time.Second
	}
	if config.RuntimeRenewBefore <= 0 {
		config.RuntimeRenewBefore = 2 * time.Minute
	}
	if config.RPCDeadline <= 0 {
		config.RPCDeadline = 5 * time.Second
	}
	if config.BackoffMax <= 0 {
		config.BackoffMax = 30 * time.Second
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.RetryJitter == nil {
		config.RetryJitter = func(window time.Duration) time.Duration {
			if window <= 0 {
				return 0
			}
			return time.Duration(rand.Int64N(int64(window) + 1))
		}
	}
}

func (r *retryGate) ready(now time.Time) bool {
	return r.next.IsZero() || !now.Before(r.next)
}

func (r *retryGate) record(now time.Time, succeeded bool, base, maximum time.Duration, jitter func(time.Duration) time.Duration) {
	if succeeded {
		r.failures = 0
		r.next = time.Time{}
		return
	}
	r.failures++
	delay := retryDelay(r.failures, base, maximum, jitter)
	r.next = now.Add(delay)
}

func retryDelay(failures uint, base, maximum time.Duration, jitter func(time.Duration) time.Duration) time.Duration {
	delay := base
	for count := uint(1); count < failures && delay < maximum; count++ {
		if delay > maximum/2 {
			delay = maximum
			break
		}
		delay *= 2
	}
	if delay > maximum {
		delay = maximum
	}
	window := delay / 4
	offset := jitter(window)
	if offset < 0 {
		offset = 0
	}
	if offset > window {
		offset %= window + 1
	}
	return delay - offset
}
