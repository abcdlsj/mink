package host

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	computerv1 "github.com/abcdlsj/sumi/gen/go/sumi/computer/v1"
	deliveryv1 "github.com/abcdlsj/sumi/gen/go/sumi/delivery/v1"
	inboxv1 "github.com/abcdlsj/sumi/gen/go/sumi/inbox/v1"
	placementv1 "github.com/abcdlsj/sumi/gen/go/sumi/placement/v1"
	runtimev1 "github.com/abcdlsj/sumi/gen/go/sumi/runtime/v1"
	spacev1 "github.com/abcdlsj/sumi/gen/go/sumi/space/v1"
	computerstate "github.com/abcdlsj/sumi/internal/computer/state"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestDaemonSlowOutboxDoesNotStarveHeartbeatOrRunRenew(t *testing.T) {
	fixture := newDaemonFixture(t)
	defer fixture.state.Close()
	fixture.daemon.config.RPCDeadline = 5 * time.Second
	now := time.Now()
	executionAgent := uuid.NewString()
	outboxAgent := uuid.NewString()
	for index, agentID := range []string{executionAgent, outboxAgent} {
		if err := fixture.state.SaveRuntimeSession(context.Background(), computerstate.RuntimeSession{
			AgentID: agentID, ComputerID: fixture.identity.ComputerID, PlacementGeneration: 1,
			Token: testRuntimeToken(byte(index + 1)), ExpiresAt: now.Add(time.Minute), UpdatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	runID := uuid.NewString()
	launchID := uuid.NewString()
	deliveryID := uuid.NewString()
	launch := &deliveryv1.RunLaunch{
		Id: launchID, RunId: runID, AgentId: executionAgent,
		HolderComputerId: fixture.identity.ComputerID, HolderPlacementGeneration: 1, Fence: 3,
		ClaimedAt: timestamppb.New(now), ExpiresAt: timestamppb.New(now.Add(500 * time.Millisecond)),
	}
	fixture.placements.list = func(context.Context) ([]*placementv1.AgentPlacement, error) {
		return []*placementv1.AgentPlacement{
			activePlacement(executionAgent, fixture.identity.ComputerID, 1),
			activePlacement(outboxAgent, fixture.identity.ComputerID, 1),
		}, nil
	}
	fixture.deliveries.list = func(request *connect.Request[deliveryv1.ListDeliveriesRequest]) (*deliveryv1.ListDeliveriesResponse, error) {
		if bearer(request.Header().Get("Authorization")) != testRuntimeToken(1) {
			return &deliveryv1.ListDeliveriesResponse{}, nil
		}
		return &deliveryv1.ListDeliveriesResponse{
			ActiveDelivery: activeDelivery(deliveryID, executionAgent),
			ActiveRun:      activeRun(runID, deliveryID, executionAgent),
			ActiveLaunch:   launch,
		}, nil
	}
	renewed := make(chan struct{}, 16)
	fixture.deliveries.renew = func(request *connect.Request[deliveryv1.RenewRunRequest]) (*deliveryv1.RunLaunch, error) {
		fixture.deliveries.incrementRenew()
		select {
		case renewed <- struct{}{}:
		default:
		}
		return &deliveryv1.RunLaunch{
			Id: request.Msg.GetLaunchId(), RunId: request.Msg.GetRunId(), AgentId: executionAgent,
			HolderComputerId: fixture.identity.ComputerID, HolderPlacementGeneration: 1,
			Fence: request.Msg.GetFence(), ClaimedAt: timestamppb.New(time.Now().Add(-time.Second)),
			ExpiresAt: timestamppb.New(time.Now().Add(500 * time.Millisecond)),
		}, nil
	}
	heartbeat := make(chan struct{}, 32)
	fixture.computers.heartbeat = func(int) error {
		select {
		case heartbeat <- struct{}{}:
		default:
		}
		return nil
	}
	completeStarted := make(chan struct{})
	releaseComplete := make(chan struct{})
	completeFinished := make(chan struct{}, 1)
	var startOnce sync.Once
	fixture.deliveries.complete = func(ctx context.Context, request *connect.Request[deliveryv1.CompleteRunRequest]) (*deliveryv1.CompleteRunResponse, error) {
		startOnce.Do(func() { close(completeStarted) })
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-releaseComplete:
			completeFinished <- struct{}{}
			return canonicalCompleteResponse(request.Msg, outboxAgent), nil
		}
	}
	if err := fixture.state.EnqueueOutbox(context.Background(), computerstate.OutboxEvent{
		OutboxEventID: uuid.NewString(), RequestID: uuid.NewString(), AgentID: outboxAgent,
		PlacementGeneration: 1, RunID: uuid.NewString(), LaunchID: uuid.NewString(), Fence: 1,
		Outcome: "succeeded", Body: "slow outbox", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	fixture.daemon.config.Executor = blockingExecutor{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- fixture.daemon.Run(ctx) }()
	select {
	case <-completeStarted:
	case <-time.After(time.Second):
		t.Fatal("outbox Complete did not start")
	}
	drainSignals(heartbeat)
	drainSignals(renewed)
	waitForSignals(t, heartbeat, 3, time.Second, "heartbeats during blocked Complete")
	waitForSignals(t, renewed, 3, time.Second, "Run renews during blocked Complete")
	select {
	case <-completeFinished:
		t.Fatal("Complete returned before the test released it")
	default:
	}
	close(releaseComplete)
	waitForSignals(t, completeFinished, 1, time.Second, "released Complete")
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestDaemonHeartbeatDeclaresTrustedLocalCapability(t *testing.T) {
	fixture := newDaemonFixture(t)
	defer fixture.state.Close()
	if err := fixture.daemon.heartbeat(context.Background(), fixture.identity); err != nil {
		t.Fatal(err)
	}
	if capability := fixture.computers.sandboxCapability(); !proto.Equal(capability, mustTrustedLocalSandboxCapability(t)) {
		t.Fatalf("heartbeat sandbox capability = %+v", capability)
	}
}

func drainSignals(signals <-chan struct{}) {
	for {
		select {
		case <-signals:
		default:
			return
		}
	}
}

func waitForSignals(t *testing.T, signals <-chan struct{}, count int, timeout time.Duration, description string) {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for received := 0; received < count; received++ {
		select {
		case <-signals:
		case <-timer.C:
			t.Fatalf("timed out waiting for %s: received %d, want %d", description, received, count)
		}
	}
}

func TestDaemonNilExecutorDoesNotObserveAcceptOrClaim(t *testing.T) {
	fixture := newDaemonFixture(t)
	defer fixture.state.Close()
	agentID := uuid.NewString()
	now := time.Now()
	if err := fixture.state.SaveRuntimeSession(context.Background(), computerstate.RuntimeSession{
		AgentID: agentID, ComputerID: fixture.identity.ComputerID, PlacementGeneration: 1,
		Token: testRuntimeToken(7), ExpiresAt: now.Add(time.Minute), UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	fixture.placements.list = func(context.Context) ([]*placementv1.AgentPlacement, error) {
		return []*placementv1.AgentPlacement{activePlacement(agentID, fixture.identity.ComputerID, 1)}, nil
	}
	fixture.deliveries.list = func(*connect.Request[deliveryv1.ListDeliveriesRequest]) (*deliveryv1.ListDeliveriesResponse, error) {
		fixture.deliveries.incrementList()
		return &deliveryv1.ListDeliveriesResponse{Deliveries: []*deliveryv1.Delivery{{Id: uuid.NewString(), AgentId: agentID}}}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := fixture.daemon.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if fixture.deliveries.listCount() != 0 || fixture.deliveries.acceptCount() != 0 || fixture.deliveries.claimCount() != 0 || fixture.inbox.count() != 0 {
		t.Fatalf("nil executor activity: list=%d accept=%d claim=%d observe=%d",
			fixture.deliveries.listCount(), fixture.deliveries.acceptCount(), fixture.deliveries.claimCount(), fixture.inbox.count())
	}
}

func TestDaemonIneligibleExecutorDoesNotListObserveAcceptOrClaim(t *testing.T) {
	fixture := newDaemonFixture(t)
	defer fixture.state.Close()
	agentID := uuid.NewString()
	now := time.Now()
	if err := fixture.state.SaveRuntimeSession(context.Background(), computerstate.RuntimeSession{
		AgentID: agentID, ComputerID: fixture.identity.ComputerID, PlacementGeneration: 1,
		Token: testRuntimeToken(42), ExpiresAt: now.Add(time.Minute), UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	fixture.daemon.config.Executor = eligibleTestExecutor{eligible: false}
	if err := fixture.daemon.dispatchDeliveries(context.Background(), fixture.identity); err != nil {
		t.Fatal(err)
	}
	if fixture.deliveries.listCount() != 0 || fixture.deliveries.acceptCount() != 0 || fixture.deliveries.claimCount() != 0 || fixture.inbox.count() != 0 {
		t.Fatalf("ineligible executor activity: list=%d accept=%d claim=%d observe=%d",
			fixture.deliveries.listCount(), fixture.deliveries.acceptCount(), fixture.deliveries.claimCount(), fixture.inbox.count())
	}
}

func TestIneligibleExecutorDoesNotStartNewWorkAndReplaysPendingOutbox(t *testing.T) {
	fixture := newDaemonFixture(t)
	defer fixture.state.Close()
	agentID := uuid.NewString()
	now := time.Now()
	if err := fixture.state.SaveRuntimeSession(context.Background(), computerstate.RuntimeSession{
		AgentID: agentID, ComputerID: fixture.identity.ComputerID, PlacementGeneration: 1,
		Token: testRuntimeToken(43), ExpiresAt: now.Add(time.Minute), UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	fixture.daemon.config.Executor = eligibleTestExecutor{eligible: false}
	event := computerstate.OutboxEvent{
		OutboxEventID: uuid.NewString(), RequestID: uuid.NewString(), AgentID: agentID, PlacementGeneration: 1,
		RunID: uuid.NewString(), LaunchID: uuid.NewString(), Fence: 1, Outcome: "succeeded", Body: "completed before unconfigure", CreatedAt: now,
	}
	if err := fixture.state.EnqueueOutbox(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	completed := 0
	fixture.deliveries.complete = func(_ context.Context, request *connect.Request[deliveryv1.CompleteRunRequest]) (*deliveryv1.CompleteRunResponse, error) {
		completed++
		return canonicalCompleteResponse(request.Msg, agentID), nil
	}
	if err := fixture.daemon.dispatchDeliveries(context.Background(), fixture.identity); err != nil {
		t.Fatal(err)
	}
	if fixture.deliveries.listCount() != 0 || fixture.deliveries.acceptCount() != 0 || fixture.deliveries.claimCount() != 0 || fixture.inbox.count() != 0 {
		t.Fatalf("ineligible new-work activity: list=%d accept=%d claim=%d observe=%d",
			fixture.deliveries.listCount(), fixture.deliveries.acceptCount(), fixture.deliveries.claimCount(), fixture.inbox.count())
	}
	replayErr := fixture.daemon.dispatchOutbox(context.Background())
	if replayErr != nil || completed != 1 {
		t.Fatalf("pending outbox replay error = %v, complete=%d", replayErr, completed)
	}
	events, err := fixture.state.Outbox(context.Background())
	if err != nil || len(events) != 0 {
		t.Fatalf("outbox after replay = %+v, %v", events, err)
	}
}

func TestAuthoritativeTriggerRejectsMissingDuplicateAndOversizeInput(t *testing.T) {
	delivery := availableDelivery(uuid.NewString(), uuid.NewString())
	valid := func() *inboxv1.ObserveTargetResponse {
		return &inboxv1.ObserveTargetResponse{
			Target: delivery.GetTarget(), HeadSequence: delivery.GetTriggerTargetSequence(),
			Messages: []*spacev1.Message{{
				Id: delivery.GetTriggerMessageId(), SpaceId: delivery.GetSpaceId(), TargetSequence: delivery.GetTriggerTargetSequence(), Body: "authoritative trigger",
			}},
		}
	}
	trigger, err := authoritativeTrigger(delivery, valid())
	if err != nil || trigger.spaceID != delivery.GetSpaceId() || trigger.observedHead != 1 || trigger.body != "authoritative trigger" {
		t.Fatalf("valid trigger = %+v, %v", trigger, err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*inboxv1.ObserveTargetResponse)
	}{
		{name: "missing", mutate: func(response *inboxv1.ObserveTargetResponse) { response.Messages = nil }},
		{name: "duplicate", mutate: func(response *inboxv1.ObserveTargetResponse) {
			response.Messages = append(response.Messages, proto.Clone(response.Messages[0]).(*spacev1.Message))
		}},
		{name: "over budget", mutate: func(response *inboxv1.ObserveTargetResponse) {
			response.Messages[0].Body = strings.Repeat("x", 400_001)
		}},
		{name: "wrong sequence", mutate: func(response *inboxv1.ObserveTargetResponse) { response.Messages[0].TargetSequence++ }},
		{name: "wrong target", mutate: func(response *inboxv1.ObserveTargetResponse) {
			response.Target = &spacev1.MessageTarget{Target: &spacev1.MessageTarget_ThreadRootMessageId{ThreadRootMessageId: uuid.NewString()}}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := valid()
			test.mutate(response)
			if trigger, err := authoritativeTrigger(delivery, response); err == nil {
				t.Fatalf("invalid trigger accepted = %+v", trigger)
			}
		})
	}
}

func TestDaemonDoesNotAcceptOrExecuteInvalidObservedTrigger(t *testing.T) {
	fixture := newDaemonFixture(t)
	defer fixture.state.Close()
	agentID := uuid.NewString()
	session := computerstate.RuntimeSession{
		AgentID: agentID, ComputerID: fixture.identity.ComputerID, PlacementGeneration: 1,
		Token: testRuntimeToken(41), ExpiresAt: time.Now().Add(time.Minute), UpdatedAt: time.Now(),
	}
	delivery := availableDelivery(uuid.NewString(), agentID)
	fixture.deliveries.list = func(*connect.Request[deliveryv1.ListDeliveriesRequest]) (*deliveryv1.ListDeliveriesResponse, error) {
		return &deliveryv1.ListDeliveriesResponse{Deliveries: []*deliveryv1.Delivery{delivery}}, nil
	}
	fixture.inbox.observe = func(request *connect.Request[inboxv1.ObserveTargetRequest]) (*inboxv1.ObserveTargetResponse, error) {
		return &inboxv1.ObserveTargetResponse{Target: request.Msg.GetTarget(), HeadSequence: 1}, nil
	}
	executor := &countingExecutor{}
	fixture.daemon.config.Executor = executor
	if err := fixture.daemon.dispatchAgent(context.Background(), fixture.identity, session); err == nil {
		t.Fatal("invalid trigger reported dispatch success")
	}
	if fixture.deliveries.acceptCount() != 0 || fixture.deliveries.claimCount() != 0 || executor.count() != 0 {
		t.Fatalf("invalid trigger activity: accept=%d claim=%d execute=%d", fixture.deliveries.acceptCount(), fixture.deliveries.claimCount(), executor.count())
	}
}

func TestPlacementUnbindRequiresSuccessfulCompleteSnapshot(t *testing.T) {
	fixture := newDaemonFixture(t)
	defer fixture.state.Close()
	agentID := uuid.NewString()
	now := time.Now()
	if err := fixture.state.SaveRuntimeSession(context.Background(), computerstate.RuntimeSession{
		AgentID: agentID, ComputerID: fixture.identity.ComputerID, PlacementGeneration: 1,
		Token: testRuntimeToken(8), ExpiresAt: now.Add(time.Minute), UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	fixture.placements.list = func(context.Context) ([]*placementv1.AgentPlacement, error) {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("offline"))
	}
	if err := fixture.daemon.syncPlacements(context.Background(), fixture.identity); err == nil {
		t.Fatal("failed snapshot reported success")
	}
	if _, found, err := fixture.state.RuntimeSession(context.Background(), agentID); err != nil || !found {
		t.Fatalf("runtime after failed snapshot = %v, %v", found, err)
	}
	fixture.placements.list = func(context.Context) ([]*placementv1.AgentPlacement, error) { return nil, nil }
	if err := fixture.daemon.syncPlacements(context.Background(), fixture.identity); err != nil {
		t.Fatal(err)
	}
	if _, found, err := fixture.state.RuntimeSession(context.Background(), agentID); err != nil || found {
		t.Fatalf("runtime after complete empty snapshot = %v, %v", found, err)
	}
}

func TestPlacementSnapshotRotatesRuntimeOnlyForCurrentBinding(t *testing.T) {
	fixture := newDaemonFixture(t)
	defer fixture.state.Close()
	agentID := uuid.NewString()
	now := time.Now()
	if err := fixture.state.SaveRuntimeSession(context.Background(), computerstate.RuntimeSession{
		AgentID: agentID, ComputerID: fixture.identity.ComputerID, PlacementGeneration: 1,
		Token: testRuntimeToken(30), ExpiresAt: now.Add(time.Minute), UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	fixture.daemon.config.Executor = cancelCompletionExecutor{}
	workerRunID := uuid.NewString()
	workerLaunchID := uuid.NewString()
	fixture.daemon.startWorker(context.Background(), computerstate.RuntimeSession{
		AgentID: agentID, ComputerID: fixture.identity.ComputerID, PlacementGeneration: 1,
	}, activeDelivery(uuid.NewString(), agentID), activeRun(workerRunID, uuid.NewString(), agentID),
		testLaunch(workerLaunchID, workerRunID, agentID, fixture.identity.ComputerID, 1, 1, now.Add(time.Minute)), testTrigger(agentID))
	fixture.placements.list = func(context.Context) ([]*placementv1.AgentPlacement, error) {
		return []*placementv1.AgentPlacement{activePlacement(agentID, fixture.identity.ComputerID, 2)}, nil
	}
	createCount := 0
	fixture.runtimes.create = func(request *connect.Request[runtimev1.CreateAgentRuntimeSessionRequest]) (*runtimev1.AgentRuntimeSession, error) {
		createCount++
		return &runtimev1.AgentRuntimeSession{
			AgentId: request.Msg.GetAgentId(), ComputerId: request.Msg.GetComputerId(),
			PlacementGeneration: request.Msg.GetPlacementGeneration(), Token: testRuntimeToken(byte(30 + createCount)),
			ExpiresAt: timestamppb.New(time.Now().Add(time.Duration(createCount) * time.Minute)),
		}, nil
	}
	if err := fixture.daemon.syncPlacements(context.Background(), fixture.identity); err != nil {
		t.Fatal(err)
	}
	session, found, err := fixture.state.RuntimeSession(context.Background(), agentID)
	if err != nil || !found || session.PlacementGeneration != 2 || session.Token != testRuntimeToken(31) {
		t.Fatalf("rotated session = %+v, %v, %v", session, found, err)
	}
	waitFor(t, time.Second, func() bool {
		fixture.daemon.workersMu.Lock()
		defer fixture.daemon.workersMu.Unlock()
		return len(fixture.daemon.workers) == 0
	})
	if events, err := fixture.state.Outbox(context.Background()); err != nil || len(events) != 0 {
		t.Fatalf("old generation worker outbox = %+v, %v", events, err)
	}
	fixture.runtimes.renew = func(*connect.Request[runtimev1.RenewAgentRuntimeSessionRequest]) (*runtimev1.AgentRuntimeSession, error) {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("revoked"))
	}
	if err := fixture.daemon.syncPlacements(context.Background(), fixture.identity); err != nil {
		t.Fatal(err)
	}
	session, found, err = fixture.state.RuntimeSession(context.Background(), agentID)
	if err != nil || !found || session.PlacementGeneration != 2 || session.Token != testRuntimeToken(32) || createCount != 2 {
		t.Fatalf("reminted session = %+v, creates=%d, %v, %v", session, createCount, found, err)
	}
	fixture.placements.list = func(context.Context) ([]*placementv1.AgentPlacement, error) {
		return []*placementv1.AgentPlacement{
			activePlacement(agentID, fixture.identity.ComputerID, 2),
			activePlacement(agentID, fixture.identity.ComputerID, 3),
		}, nil
	}
	if err := fixture.daemon.syncPlacements(context.Background(), fixture.identity); err == nil {
		t.Fatal("duplicate authoritative snapshot reported success")
	}
	unchanged, found, err := fixture.state.RuntimeSession(context.Background(), agentID)
	if err != nil || !found || unchanged.Token != session.Token || unchanged.PlacementGeneration != 2 || createCount != 2 {
		t.Fatalf("runtime changed after invalid snapshot = %+v, creates=%d, %v, %v", unchanged, createCount, found, err)
	}
}

func TestExecutorErrorAndCancelCreateNoOutbox(t *testing.T) {
	mentionID := uuid.NewString()
	tooManyMentions := make([]string, 65)
	for index := range tooManyMentions {
		tooManyMentions[index] = uuid.NewString()
	}
	tests := []struct {
		name     string
		executor Executor
		cancel   bool
	}{
		{name: "technical error", executor: errorExecutor{}},
		{name: "shutdown cancel", executor: blockingExecutor{}, cancel: true},
		{name: "completion racing shutdown", executor: cancelCompletionExecutor{}, cancel: true},
		{name: "empty completion body", executor: completionExecutor{completion: Completion{Outcome: deliveryv1.RunOutcome_RUN_OUTCOME_SUCCEEDED}}},
		{name: "invalid completion utf8", executor: completionExecutor{completion: Completion{Outcome: deliveryv1.RunOutcome_RUN_OUTCOME_SUCCEEDED, Body: string([]byte{0xff})}}},
		{name: "completion body too long", executor: completionExecutor{completion: Completion{Outcome: deliveryv1.RunOutcome_RUN_OUTCOME_SUCCEEDED, Body: strings.Repeat("x", 400_001)}}},
		{name: "too many completion mentions", executor: completionExecutor{completion: Completion{Outcome: deliveryv1.RunOutcome_RUN_OUTCOME_SUCCEEDED, Body: "result", MentionedAgentIDs: tooManyMentions}}},
		{name: "duplicate completion mentions", executor: completionExecutor{completion: Completion{Outcome: deliveryv1.RunOutcome_RUN_OUTCOME_SUCCEEDED, Body: "result", MentionedAgentIDs: []string{mentionID, mentionID}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newDaemonFixture(t)
			defer fixture.state.Close()
			agentID := uuid.NewString()
			runID := uuid.NewString()
			launchID := uuid.NewString()
			now := time.Now()
			fixture.daemon.config.Executor = test.executor
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			fixture.daemon.startWorker(ctx, computerstate.RuntimeSession{
				AgentID: agentID, ComputerID: fixture.identity.ComputerID, PlacementGeneration: 1,
			}, &deliveryv1.Delivery{Id: uuid.NewString()}, &deliveryv1.Run{Id: runID}, &deliveryv1.RunLaunch{
				Id: launchID, RunId: runID, AgentId: agentID, HolderComputerId: fixture.identity.ComputerID,
				HolderPlacementGeneration: 1, Fence: 1, ExpiresAt: timestamppb.New(now.Add(time.Second)),
			}, testTrigger(agentID))
			if test.cancel {
				cancel()
			}
			waitFor(t, time.Second, func() bool {
				fixture.daemon.workersMu.Lock()
				defer fixture.daemon.workersMu.Unlock()
				return len(fixture.daemon.workers) == 0
			})
			events, err := fixture.state.Outbox(context.Background())
			if err != nil || len(events) != 0 {
				t.Fatalf("outbox after executor stop = %+v, %v", events, err)
			}
		})
	}
}

func TestReconcileActiveMutationsRefreshesRunningWorkerLease(t *testing.T) {
	fixture := newDaemonFixture(t)
	defer fixture.state.Close()
	agentID := uuid.NewString()
	deliveryID := uuid.NewString()
	runID := uuid.NewString()
	launchID := uuid.NewString()
	now := time.Now()
	executor := newBlockingCountingExecutor()
	fixture.daemon.config.Executor = executor
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session := computerstate.RuntimeSession{
		AgentID: agentID, ComputerID: fixture.identity.ComputerID, PlacementGeneration: 1,
		Token: testRuntimeToken(1), ExpiresAt: now.Add(time.Minute), UpdatedAt: now,
	}
	if err := fixture.state.SaveRuntimeSession(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	delivery := activeDelivery(deliveryID, agentID)
	run := activeRun(runID, deliveryID, agentID)
	launch := testLaunch(launchID, runID, agentID, fixture.identity.ComputerID, 1, 1, now.Add(2*time.Second))
	fixture.daemon.startWorker(ctx, session, delivery, run, launch, testTrigger(agentID))
	select {
	case <-executor.started:
	case <-time.After(time.Second):
		t.Fatal("executor did not start")
	}
	if err := fixture.daemon.reconcileActiveMutations(context.Background(), session, delivery, run, testLaunch(launchID, runID, agentID, fixture.identity.ComputerID, 1, 1, now.Add(5*time.Second))); err != nil {
		t.Fatal(err)
	}
	fixture.daemon.workersMu.Lock()
	workerExpiry := fixture.daemon.workers[runID].leaseExpiry.Load()
	fixture.daemon.workersMu.Unlock()
	if !time.Unix(0, workerExpiry).After(now.Add(900 * time.Millisecond)) {
		t.Fatalf("worker lease was not refreshed: %v", time.Unix(0, workerExpiry))
	}
	time.Sleep(2500 * time.Millisecond)
	fixture.daemon.startWorker(ctx, session, delivery, run, testLaunch(launchID, runID, agentID, fixture.identity.ComputerID, 1, 1, now.Add(5*time.Second)), testTrigger(agentID))
	if calls := executor.count(); calls != 1 {
		t.Fatalf("executor calls after initial lease expiry = %d, want 1", calls)
	}
	fixture.daemon.workersMu.Lock()
	workerCount := len(fixture.daemon.workers)
	fixture.daemon.workersMu.Unlock()
	if workerCount != 1 {
		t.Fatalf("workers after reconciled lease expiry = %d, want 1", workerCount)
	}
	cancel()
	waitFor(t, time.Second, func() bool {
		fixture.daemon.workersMu.Lock()
		defer fixture.daemon.workersMu.Unlock()
		return len(fixture.daemon.workers) == 0
	})
	fixture.daemon.workersWG.Wait()
}

func TestExecutionBindsCurrentComputerAndRejectsStalePlacement(t *testing.T) {
	fixture := newDaemonFixture(t)
	defer fixture.state.Close()
	executor := newExecutionCaptureExecutor()
	fixture.daemon.config.Executor = executor
	agentID := uuid.NewString()
	session := computerstate.RuntimeSession{
		AgentID: agentID, ComputerID: fixture.identity.ComputerID, PlacementGeneration: 3,
	}
	deliveryID := uuid.NewString()
	runID := uuid.NewString()
	fixture.daemon.startWorker(context.Background(), session, activeDelivery(deliveryID, agentID), activeRun(runID, deliveryID, agentID),
		testLaunch(uuid.NewString(), runID, agentID, session.ComputerID, 3, 7, time.Now().Add(time.Minute)), testTrigger(agentID))
	select {
	case execution := <-executor.executions:
		if execution.ComputerID != session.ComputerID || execution.AgentID != agentID || execution.PlacementGeneration != 3 || execution.Fence != 7 {
			t.Fatalf("execution binding = %+v", execution)
		}
	case <-time.After(time.Second):
		t.Fatal("executor did not receive current binding")
	}
	fixture.daemon.workersWG.Wait()

	for _, launch := range []*deliveryv1.RunLaunch{
		testLaunch(uuid.NewString(), uuid.NewString(), agentID, uuid.NewString(), 3, 8, time.Now().Add(time.Minute)),
		testLaunch(uuid.NewString(), uuid.NewString(), agentID, session.ComputerID, 2, 9, time.Now().Add(time.Minute)),
	} {
		deliveryID := uuid.NewString()
		runID := launch.GetRunId()
		fixture.daemon.startWorker(context.Background(), session, activeDelivery(deliveryID, agentID), activeRun(runID, deliveryID, agentID), launch, testTrigger(agentID))
	}
	select {
	case execution := <-executor.executions:
		t.Fatalf("stale binding executed = %+v", execution)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestExecutorCompletionCreatesDurableOutbox(t *testing.T) {
	fixture := newDaemonFixture(t)
	defer fixture.state.Close()
	agentID := uuid.NewString()
	deliveryID := uuid.NewString()
	runID := uuid.NewString()
	launchID := uuid.NewString()
	mentionedAgentID := uuid.NewString()
	now := time.Now()
	session := computerstate.RuntimeSession{
		AgentID: agentID, ComputerID: fixture.identity.ComputerID, PlacementGeneration: 3,
		Token: testRuntimeToken(29), ExpiresAt: now.Add(time.Minute), UpdatedAt: now,
	}
	if err := fixture.state.SaveRuntimeSession(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	fixture.deliveries.renew = func(request *connect.Request[deliveryv1.RenewRunRequest]) (*deliveryv1.RunLaunch, error) {
		fixture.deliveries.incrementRenew()
		return testLaunch(request.Msg.GetLaunchId(), request.Msg.GetRunId(), agentID, fixture.identity.ComputerID, 3, request.Msg.GetFence(), now.Add(2*time.Minute)), nil
	}
	fixture.daemon.config.Executor = completionExecutor{completion: Completion{
		Outcome: deliveryv1.RunOutcome_RUN_OUTCOME_SUCCEEDED, Body: "completed body",
		MentionedAgentIDs: []string{mentionedAgentID},
	}}
	enqueueAttempts := 0
	fixture.daemon.persistOutbox = func(ctx context.Context, event computerstate.OutboxEvent) error {
		enqueueAttempts++
		if enqueueAttempts < 3 {
			return errors.New("local state temporarily unavailable")
		}
		return fixture.state.EnqueueOutbox(ctx, event)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fixture.daemon.startWorker(ctx, session, activeDelivery(deliveryID, agentID), activeRun(runID, deliveryID, agentID),
		testLaunch(launchID, runID, agentID, fixture.identity.ComputerID, 3, 9, now.Add(time.Minute)), testTrigger(agentID))
	waitFor(t, time.Second, func() bool {
		fixture.daemon.workersMu.Lock()
		defer fixture.daemon.workersMu.Unlock()
		return len(fixture.daemon.workers) == 0
	})
	events, err := fixture.state.Outbox(context.Background())
	if err != nil || len(events) != 1 {
		t.Fatalf("outbox = %+v, %v", events, err)
	}
	event := events[0]
	if event.AgentID != agentID || event.PlacementGeneration != 3 || event.RunID != runID || event.LaunchID != launchID ||
		event.Fence != 9 || event.Outcome != "succeeded" || event.Body != "completed body" ||
		len(event.MentionedAgentIDs) != 1 || event.MentionedAgentIDs[0] != mentionedAgentID {
		t.Fatalf("outbox event = %+v", event)
	}
	if enqueueAttempts != 3 {
		t.Fatalf("enqueue attempts = %d, want 3", enqueueAttempts)
	}
	if renews := fixture.deliveries.renewCount(); renews < 1 {
		t.Fatalf("Run renews while local enqueue retried = %d, want >= 1", renews)
	}
}

func TestDaemonHeartbeatBackoffRecoversWhileSnapshotIsBlocked(t *testing.T) {
	fixture := newDaemonFixture(t)
	defer fixture.state.Close()
	var mu sync.Mutex
	var attempts []time.Time
	recovered := make(chan struct{})
	fixture.computers.heartbeat = func(count int) error {
		mu.Lock()
		attempts = append(attempts, time.Now())
		mu.Unlock()
		if count == 7 {
			close(recovered)
		}
		if count <= 3 {
			return connect.NewError(connect.CodeUnavailable, errors.New("offline"))
		}
		return nil
	}
	fixture.placements.list = func(ctx context.Context) ([]*placementv1.AgentPlacement, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- fixture.daemon.Run(ctx) }()
	select {
	case <-recovered:
		cancel()
	case <-ctx.Done():
		t.Fatal("heartbeat did not recover")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(attempts) < 7 {
		t.Fatalf("heartbeat attempts = %d, want at least 7", len(attempts))
	}
	minimumGaps := []time.Duration{7 * time.Millisecond, 14 * time.Millisecond, 28 * time.Millisecond}
	for index, minimum := range minimumGaps {
		gap := attempts[index+1].Sub(attempts[index])
		if gap < minimum {
			t.Fatalf("heartbeat retry gap %d = %s, want at least %s", index+1, gap, minimum)
		}
	}
}

func TestDaemonMutationJournalReplaysAcrossRestartAndAdvancesRenewRequest(t *testing.T) {
	fixture := newDaemonFixture(t)
	defer func() { _ = fixture.state.Close() }()
	now := time.Now()
	agentID := uuid.NewString()
	deliveryID := uuid.NewString()
	runID := uuid.NewString()
	launchID := uuid.NewString()
	session := computerstate.RuntimeSession{
		AgentID: agentID, ComputerID: fixture.identity.ComputerID, PlacementGeneration: 1,
		Token: testRuntimeToken(21), ExpiresAt: now.Add(time.Minute), UpdatedAt: now,
	}
	delivery := availableDelivery(deliveryID, agentID)
	var acceptRequests []string
	fixture.deliveries.accept = func(request *connect.Request[deliveryv1.AcceptDeliveryRequest]) (*deliveryv1.Run, error) {
		acceptRequests = append(acceptRequests, request.Msg.GetRequestId())
		if len(acceptRequests) == 1 {
			return nil, connect.NewError(connect.CodeUnavailable, errors.New("response lost"))
		}
		return activeRun(runID, deliveryID, agentID), nil
	}
	if run, err := fixture.daemon.acceptDelivery(context.Background(), session, delivery); run != nil || err == nil {
		t.Fatalf("first accept = %+v, %v", run, err)
	}
	reopenDaemonState(t, &fixture)
	if run, err := fixture.daemon.acceptDelivery(context.Background(), session, delivery); err != nil || run == nil || run.GetId() != runID {
		t.Fatalf("replayed accept = %+v, %v", run, err)
	}
	if len(acceptRequests) != 2 || acceptRequests[0] != acceptRequests[1] {
		t.Fatalf("accept request ids = %v", acceptRequests)
	}

	var claimRequests []string
	claimExpiry := now.Add(time.Minute)
	fixture.deliveries.claim = func(request *connect.Request[deliveryv1.ClaimRunRequest]) (*deliveryv1.RunLaunch, error) {
		claimRequests = append(claimRequests, request.Msg.GetRequestId())
		if len(claimRequests) == 1 {
			return nil, connect.NewError(connect.CodeUnavailable, errors.New("response lost"))
		}
		return testLaunch(launchID, runID, agentID, fixture.identity.ComputerID, 1, 4, claimExpiry), nil
	}
	if launch, err := fixture.daemon.claimRun(context.Background(), session, runID); launch != nil || err == nil {
		t.Fatalf("first claim = %+v, %v", launch, err)
	}
	reopenDaemonState(t, &fixture)
	if launch, err := fixture.daemon.claimRun(context.Background(), session, runID); err != nil || launch == nil || launch.GetId() != launchID {
		t.Fatalf("replayed claim = %+v, %v", launch, err)
	}
	if len(claimRequests) != 2 || claimRequests[0] != claimRequests[1] {
		t.Fatalf("claim request ids = %v", claimRequests)
	}

	var renewRequests []string
	fixture.deliveries.renew = func(request *connect.Request[deliveryv1.RenewRunRequest]) (*deliveryv1.RunLaunch, error) {
		renewRequests = append(renewRequests, request.Msg.GetRequestId())
		if len(renewRequests) == 1 {
			return nil, connect.NewError(connect.CodeUnavailable, errors.New("response lost"))
		}
		expiresAt := claimExpiry.Add(time.Duration(len(renewRequests)-1) * time.Minute)
		return testLaunch(launchID, runID, agentID, fixture.identity.ComputerID, 1, 4, expiresAt), nil
	}
	if _, err := fixture.daemon.renewRun(context.Background(), session, runID, launchID, 4, claimExpiry); err == nil {
		t.Fatal("first renew succeeded")
	}
	reopenDaemonState(t, &fixture)
	firstRenewExpiry, err := fixture.daemon.renewRun(context.Background(), session, runID, launchID, 4, claimExpiry)
	if err != nil {
		t.Fatal(err)
	}
	secondRenewExpiry, err := fixture.daemon.renewRun(context.Background(), session, runID, launchID, 4, firstRenewExpiry)
	if err != nil || !secondRenewExpiry.After(firstRenewExpiry) {
		t.Fatalf("next renew = %s, %v", secondRenewExpiry, err)
	}
	if len(renewRequests) != 3 || renewRequests[0] != renewRequests[1] || renewRequests[2] == renewRequests[1] {
		t.Fatalf("renew request ids = %v", renewRequests)
	}

	recoveryDeliveryID := uuid.NewString()
	recoveryRunID := uuid.NewString()
	var unauthenticatedRequests []string
	fixture.deliveries.accept = func(request *connect.Request[deliveryv1.AcceptDeliveryRequest]) (*deliveryv1.Run, error) {
		unauthenticatedRequests = append(unauthenticatedRequests, request.Msg.GetRequestId())
		if len(unauthenticatedRequests) == 1 {
			return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("runtime expired"))
		}
		return activeRun(recoveryRunID, recoveryDeliveryID, agentID), nil
	}
	if run, err := fixture.daemon.acceptDelivery(context.Background(), session, availableDelivery(recoveryDeliveryID, agentID)); run != nil || err == nil {
		t.Fatalf("unauthenticated accept = %+v, %v", run, err)
	}
	if run, err := fixture.daemon.acceptDelivery(context.Background(), session, availableDelivery(recoveryDeliveryID, agentID)); err != nil || run == nil {
		t.Fatalf("accept after runtime recovery = %+v, %v", run, err)
	}
	if len(unauthenticatedRequests) != 2 || unauthenticatedRequests[0] != unauthenticatedRequests[1] {
		t.Fatalf("unauthenticated request ids = %v", unauthenticatedRequests)
	}
}

func TestDaemonReconcilesLostMutationResponsesFromActiveFactsBeforeNewFence(t *testing.T) {
	fixture := newDaemonFixture(t)
	defer func() { _ = fixture.state.Close() }()
	now := time.Now()
	agentID := uuid.NewString()
	deliveryID := uuid.NewString()
	runID := uuid.NewString()
	oldLaunchID := uuid.NewString()
	session := computerstate.RuntimeSession{
		AgentID: agentID, ComputerID: fixture.identity.ComputerID, PlacementGeneration: 2,
		Token: testRuntimeToken(26), ExpiresAt: now.Add(time.Minute), UpdatedAt: now,
	}
	if err := fixture.state.SaveRuntimeSession(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	acceptAttempt, err := fixture.state.BeginMutation(context.Background(), computerstate.MutationAttempt{
		Operation: "delivery.accept", SubjectID: deliveryID, PayloadHash: mutationHash("delivery.accept", deliveryID), CreatedAt: now.Add(-time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	claimAttempt, err := fixture.state.BeginMutation(context.Background(), computerstate.MutationAttempt{
		Operation: "run.claim", SubjectID: claimSubject(runID, session),
		PayloadHash: mutationHash("run.claim", runID, session.ComputerID, "2"), RunID: runID, CreatedAt: now.Add(-time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	renewAttempt, err := fixture.state.BeginMutation(context.Background(), computerstate.MutationAttempt{
		Operation: "run.renew", SubjectID: oldLaunchID,
		PayloadHash: mutationHash("run.renew", runID, oldLaunchID, "4", session.ComputerID, "2"),
		RunID:       runID, LaunchID: oldLaunchID, Fence: 4, CreatedAt: now.Add(-time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	reopenDaemonState(t, &fixture)
	restartFixtureDaemon(&fixture)
	oldLaunch := testLaunch(oldLaunchID, runID, agentID, session.ComputerID, 2, 4, now.Add(-time.Second))
	fixture.deliveries.list = func(*connect.Request[deliveryv1.ListDeliveriesRequest]) (*deliveryv1.ListDeliveriesResponse, error) {
		return &deliveryv1.ListDeliveriesResponse{
			ActiveDelivery: activeDelivery(deliveryID, agentID),
			ActiveRun:      activeRun(runID, deliveryID, agentID),
			ActiveLaunch:   oldLaunch,
		}, nil
	}
	var newClaimRequestID string
	newLaunchID := uuid.NewString()
	fixture.deliveries.claim = func(request *connect.Request[deliveryv1.ClaimRunRequest]) (*deliveryv1.RunLaunch, error) {
		newClaimRequestID = request.Msg.GetRequestId()
		return testLaunch(newLaunchID, runID, agentID, session.ComputerID, 2, 5, now.Add(time.Minute)), nil
	}
	fixture.daemon.config.Executor = blockingExecutor{}
	if err := fixture.daemon.dispatchAgent(context.Background(), fixture.identity, session); err != nil {
		t.Fatal(err)
	}
	if newClaimRequestID == "" || newClaimRequestID == claimAttempt.RequestID {
		t.Fatalf("new claim request id = %q, old = %q", newClaimRequestID, claimAttempt.RequestID)
	}
	for _, check := range []struct {
		operation computerstate.MutationOperation
		subject   string
		requestID string
	}{
		{computerstate.MutationDeliveryAccept, deliveryID, acceptAttempt.RequestID},
		{computerstate.MutationRunClaim, claimSubject(runID, session), claimAttempt.RequestID},
		{computerstate.MutationRunRenew, oldLaunchID, renewAttempt.RequestID},
	} {
		attempts, err := fixture.state.MutationAttempts(context.Background(), check.operation, check.subject)
		if err != nil {
			t.Fatal(err)
		}
		if status := mutationStatus(attempts, check.requestID); status != "succeeded" {
			t.Fatalf("%s %s status = %q, attempts=%+v", check.operation, check.requestID, status, attempts)
		}
	}
	claimAttempts, err := fixture.state.MutationAttempts(context.Background(), "run.claim", claimSubject(runID, session))
	if err != nil || mutationStatus(claimAttempts, newClaimRequestID) != "succeeded" {
		t.Fatalf("new claim attempts = %+v, %v", claimAttempts, err)
	}
	fixture.daemon.stopAllWorkers()
	fixture.daemon.workersWG.Wait()
}

func TestDaemonClaimJournalIsScopedToPlacementBinding(t *testing.T) {
	fixture := newDaemonFixture(t)
	defer fixture.state.Close()
	now := time.Now()
	agentID := uuid.NewString()
	runID := uuid.NewString()
	deliveryID := uuid.NewString()
	gen1 := computerstate.RuntimeSession{
		AgentID: agentID, ComputerID: fixture.identity.ComputerID, PlacementGeneration: 1,
		Token: testRuntimeToken(33), ExpiresAt: now.Add(time.Minute), UpdatedAt: now,
	}
	oldAttempt, err := fixture.state.BeginMutation(context.Background(), computerstate.MutationAttempt{
		Operation: "run.claim", SubjectID: claimSubject(runID, gen1),
		PayloadHash: mutationHash("run.claim", runID, gen1.ComputerID, "1"), RunID: runID, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	gen2 := gen1
	gen2.PlacementGeneration = 2
	gen2.Token = testRuntimeToken(34)
	gen2.UpdatedAt = now.Add(time.Second)
	if err := fixture.state.SaveRuntimeSession(context.Background(), gen2); err != nil {
		t.Fatal(err)
	}
	fixture.deliveries.list = func(*connect.Request[deliveryv1.ListDeliveriesRequest]) (*deliveryv1.ListDeliveriesResponse, error) {
		return &deliveryv1.ListDeliveriesResponse{
			ActiveDelivery: activeDelivery(deliveryID, agentID),
			ActiveRun: &deliveryv1.Run{
				Id: runID, DeliveryId: deliveryID, AgentId: agentID, BasisTargetSequence: 1, State: deliveryv1.RunState_RUN_STATE_ACCEPTED,
			},
		}, nil
	}
	var newRequestID string
	fixture.deliveries.claim = func(request *connect.Request[deliveryv1.ClaimRunRequest]) (*deliveryv1.RunLaunch, error) {
		newRequestID = request.Msg.GetRequestId()
		return testLaunch(uuid.NewString(), runID, agentID, gen2.ComputerID, 2, 2, now.Add(time.Minute)), nil
	}
	fixture.daemon.config.Executor = blockingExecutor{}
	if err := fixture.daemon.dispatchAgent(context.Background(), fixture.identity, gen2); err != nil {
		t.Fatal(err)
	}
	if newRequestID == "" || newRequestID == oldAttempt.RequestID {
		t.Fatalf("gen2 claim request = %q, gen1 = %q", newRequestID, oldAttempt.RequestID)
	}
	oldAttempts, err := fixture.state.MutationAttempts(context.Background(), "run.claim", claimSubject(runID, gen1))
	if err != nil || mutationStatus(oldAttempts, oldAttempt.RequestID) != "pending" {
		t.Fatalf("gen1 claim attempts = %+v, %v", oldAttempts, err)
	}
	newAttempts, err := fixture.state.MutationAttempts(context.Background(), "run.claim", claimSubject(runID, gen2))
	if err != nil || mutationStatus(newAttempts, newRequestID) != "succeeded" {
		t.Fatalf("gen2 claim attempts = %+v, %v", newAttempts, err)
	}
	fixture.daemon.stopAllWorkers()
	fixture.daemon.workersWG.Wait()
}

func TestDaemonRejectsImpossibleActiveRunLaunchCombinations(t *testing.T) {
	tests := []struct {
		name         string
		state        deliveryv1.RunState
		launch       bool
		mutateLaunch func(*deliveryv1.RunLaunch)
	}{
		{name: "accepted with launch", state: deliveryv1.RunState_RUN_STATE_ACCEPTED, launch: true},
		{name: "running without launch", state: deliveryv1.RunState_RUN_STATE_RUNNING},
		{name: "running without claimed time", state: deliveryv1.RunState_RUN_STATE_RUNNING, launch: true, mutateLaunch: func(launch *deliveryv1.RunLaunch) {
			launch.ClaimedAt = nil
		}},
		{name: "running with inverted lease", state: deliveryv1.RunState_RUN_STATE_RUNNING, launch: true, mutateLaunch: func(launch *deliveryv1.RunLaunch) {
			launch.ExpiresAt = launch.ClaimedAt
		}},
		{name: "running with closed launch", state: deliveryv1.RunState_RUN_STATE_RUNNING, launch: true, mutateLaunch: func(launch *deliveryv1.RunLaunch) {
			launch.ClosedAt = timestamppb.Now()
			launch.CloseReason = deliveryv1.RunLaunchCloseReason_RUN_LAUNCH_CLOSE_REASON_REPLACED
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newDaemonFixture(t)
			defer fixture.state.Close()
			agentID := uuid.NewString()
			deliveryID := uuid.NewString()
			runID := uuid.NewString()
			session := computerstate.RuntimeSession{
				AgentID: agentID, ComputerID: fixture.identity.ComputerID, PlacementGeneration: 1,
				Token: testRuntimeToken(27), ExpiresAt: time.Now().Add(time.Minute), UpdatedAt: time.Now(),
			}
			executor := &countingExecutor{}
			fixture.daemon.config.Executor = executor
			fixture.deliveries.list = func(*connect.Request[deliveryv1.ListDeliveriesRequest]) (*deliveryv1.ListDeliveriesResponse, error) {
				response := &deliveryv1.ListDeliveriesResponse{
					ActiveDelivery: activeDelivery(deliveryID, agentID),
					ActiveRun: &deliveryv1.Run{
						Id: runID, DeliveryId: deliveryID, AgentId: agentID, State: test.state,
					},
				}
				if test.launch {
					response.ActiveLaunch = testLaunch(uuid.NewString(), runID, agentID, session.ComputerID, 1, 1, time.Now().Add(time.Minute))
					if test.mutateLaunch != nil {
						test.mutateLaunch(response.ActiveLaunch)
					}
				}
				return response, nil
			}
			if err := fixture.daemon.dispatchAgent(context.Background(), fixture.identity, session); err == nil {
				t.Fatal("impossible active facts reported success")
			}
			if fixture.deliveries.claimCount() != 0 || executor.count() != 0 {
				t.Fatalf("invalid facts activity: claim=%d execute=%d", fixture.deliveries.claimCount(), executor.count())
			}
		})
	}
}

func TestDaemonOutboxReplaysExactEventAfterCommittedResponseIsLost(t *testing.T) {
	fixture := newDaemonFixture(t)
	defer func() { _ = fixture.state.Close() }()
	now := time.Now()
	agentID := uuid.NewString()
	session := computerstate.RuntimeSession{
		AgentID: agentID, ComputerID: fixture.identity.ComputerID, PlacementGeneration: 2,
		Token: testRuntimeToken(22), ExpiresAt: now.Add(time.Minute), UpdatedAt: now,
	}
	if err := fixture.state.SaveRuntimeSession(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	event := computerstate.OutboxEvent{
		OutboxEventID: uuid.NewString(), RequestID: uuid.NewString(), AgentID: agentID, PlacementGeneration: 2,
		RunID: uuid.NewString(), LaunchID: uuid.NewString(), Fence: 8, Outcome: "succeeded",
		Body: "durable result", MentionedAgentIDs: []string{uuid.NewString()}, CreatedAt: now,
	}
	if err := fixture.state.EnqueueOutbox(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	var requests []*deliveryv1.CompleteRunRequest
	fixture.deliveries.complete = func(_ context.Context, request *connect.Request[deliveryv1.CompleteRunRequest]) (*deliveryv1.CompleteRunResponse, error) {
		requests = append(requests, request.Msg)
		if len(requests) == 1 {
			return nil, connect.NewError(connect.CodeUnavailable, errors.New("response lost after commit"))
		}
		return canonicalCompleteResponse(request.Msg, agentID), nil
	}
	if err := fixture.daemon.dispatchOutbox(context.Background()); err == nil {
		t.Fatal("lost response reported success")
	}
	reopenDaemonState(t, &fixture)
	if err := fixture.daemon.dispatchOutbox(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 2 || !sameCompleteRequest(requests[0], requests[1]) {
		t.Fatalf("Complete requests changed: %+v", requests)
	}
	events, err := fixture.state.Outbox(context.Background())
	if err != nil || len(events) != 0 {
		t.Fatalf("outbox after canonical replay = %+v, %v", events, err)
	}
}

func TestClaimDoesNotReturnLaunchBeforeCanonicalReceiptIsDurable(t *testing.T) {
	fixture := newDaemonFixture(t)
	defer func() { _ = fixture.state.Close() }()
	now := time.Now()
	agentID := uuid.NewString()
	runID := uuid.NewString()
	launchID := uuid.NewString()
	session := computerstate.RuntimeSession{
		AgentID: agentID, ComputerID: fixture.identity.ComputerID, PlacementGeneration: 1,
		Token: testRuntimeToken(23), ExpiresAt: now.Add(time.Minute), UpdatedAt: now,
	}
	fixture.deliveries.claim = func(*connect.Request[deliveryv1.ClaimRunRequest]) (*deliveryv1.RunLaunch, error) {
		if err := fixture.state.Close(); err != nil {
			t.Fatal(err)
		}
		return testLaunch(launchID, runID, agentID, fixture.identity.ComputerID, 1, 5, now.Add(time.Minute)), nil
	}
	if launch, err := fixture.daemon.claimRun(context.Background(), session, runID); launch != nil || err == nil {
		t.Fatalf("claim without durable receipt = %+v, %v", launch, err)
	}
	state, err := computerstate.Open(fixture.dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	fixture.state = state
	fixture.daemon.config.State = state
	attempts, err := state.MutationAttempts(context.Background(), "run.claim", claimSubject(runID, session))
	if err != nil || len(attempts) != 1 || attempts[0].Status != "pending" {
		t.Fatalf("claim attempts = %+v, %v", attempts, err)
	}
}

func TestOutboxKeepsEventWhenCanonicalResponseResultDoesNotMatch(t *testing.T) {
	fixture := newDaemonFixture(t)
	defer fixture.state.Close()
	now := time.Now()
	agentID := uuid.NewString()
	if err := fixture.state.SaveRuntimeSession(context.Background(), computerstate.RuntimeSession{
		AgentID: agentID, ComputerID: fixture.identity.ComputerID, PlacementGeneration: 1,
		Token: testRuntimeToken(24), ExpiresAt: now.Add(time.Minute), UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	event := computerstate.OutboxEvent{
		OutboxEventID: uuid.NewString(), RequestID: uuid.NewString(), AgentID: agentID, PlacementGeneration: 1,
		RunID: uuid.NewString(), LaunchID: uuid.NewString(), Fence: 1, Outcome: "failed", Body: "result", CreatedAt: now,
	}
	if err := fixture.state.EnqueueOutbox(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	fixture.deliveries.complete = func(_ context.Context, request *connect.Request[deliveryv1.CompleteRunRequest]) (*deliveryv1.CompleteRunResponse, error) {
		response := canonicalCompleteResponse(request.Msg, agentID)
		response.Run.ResultRef = &deliveryv1.Run_ResultMessageId{ResultMessageId: uuid.NewString()}
		return response, nil
	}
	if err := fixture.daemon.dispatchOutbox(context.Background()); err == nil {
		t.Fatal("mismatched response reported success")
	}
	events, err := fixture.state.Outbox(context.Background())
	if err != nil || len(events) != 1 || events[0].OutboxEventID != event.OutboxEventID || events[0].Attempts != 1 {
		t.Fatalf("outbox after mismatch = %+v, %v", events, err)
	}
}

func TestOutboxTombstonesStaleFenceAndClearsSensitivePayload(t *testing.T) {
	fixture := newDaemonFixture(t)
	defer fixture.state.Close()
	now := time.Now()
	agentID := uuid.NewString()
	mentionedAgentID := uuid.NewString()
	if err := fixture.state.SaveRuntimeSession(context.Background(), computerstate.RuntimeSession{
		AgentID: agentID, ComputerID: fixture.identity.ComputerID, PlacementGeneration: 2,
		Token: testRuntimeToken(25), ExpiresAt: now.Add(time.Minute), UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	event := computerstate.OutboxEvent{
		OutboxEventID: uuid.NewString(), RequestID: uuid.NewString(), AgentID: agentID, PlacementGeneration: 1,
		RunID: uuid.NewString(), LaunchID: uuid.NewString(), Fence: 2, Outcome: "succeeded",
		Body: "stale sensitive body", MentionedAgentIDs: []string{mentionedAgentID}, CreatedAt: now,
	}
	if err := fixture.state.EnqueueOutbox(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	fixture.deliveries.complete = func(context.Context, *connect.Request[deliveryv1.CompleteRunRequest]) (*deliveryv1.CompleteRunResponse, error) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("stale fence"))
	}
	if err := fixture.daemon.dispatchOutbox(context.Background()); err != nil {
		t.Fatal(err)
	}
	events, err := fixture.state.Outbox(context.Background())
	if err != nil || len(events) != 1 || events[0].State != "tombstone" || events[0].Body != "" ||
		len(events[0].MentionedAgentIDs) != 0 || events[0].RejectionCode != connect.CodeFailedPrecondition.String() {
		t.Fatalf("stale tombstone = %+v, %v", events, err)
	}
}

type daemonFixture struct {
	dataRoot   string
	state      *computerstate.State
	identity   computerstate.Identity
	daemon     *Daemon
	computers  *stubComputerClient
	placements *stubPlacementClient
	runtimes   *stubRuntimeClient
	inbox      *stubInboxClient
	deliveries *stubDeliveryClient
}

func newDaemonFixture(t *testing.T) daemonFixture {
	t.Helper()
	dataRoot := filepath.Join(t.TempDir(), "computer")
	state, err := computerstate.Open(dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	identity := computerstate.Identity{
		ServerURL: "https://sumi.test", ComputerID: uuid.NewString(), RegistrationKey: "registration-key", PairedAt: time.Now(),
	}
	if err := state.SaveIdentity(context.Background(), identity); err != nil {
		state.Close()
		t.Fatal(err)
	}
	computers := &stubComputerClient{}
	placements := &stubPlacementClient{}
	runtimes := &stubRuntimeClient{}
	inbox := &stubInboxClient{}
	deliveries := &stubDeliveryClient{}
	daemon := NewDaemon(DaemonConfig{
		ServerURL: identity.ServerURL, DataRoot: dataRoot, State: state,
		HeartbeatInterval: 10 * time.Millisecond, SnapshotInterval: 10 * time.Millisecond,
		DeliveryInterval: 10 * time.Millisecond, RunRenewInterval: 20 * time.Millisecond,
		OutboxInterval: 10 * time.Millisecond, RPCDeadline: 250 * time.Millisecond, BackoffMax: 40 * time.Millisecond,
		RetryJitter: func(window time.Duration) time.Duration { return window },
	})
	daemon.computers = computers
	daemon.placements = placements
	daemon.runtimes = runtimes
	daemon.inbox = inbox
	daemon.deliveries = deliveries
	return daemonFixture{dataRoot, state, identity, daemon, computers, placements, runtimes, inbox, deliveries}
}

type stubComputerClient struct {
	mu         sync.Mutex
	heartbeats int
	heartbeat  func(int) error
	capability *computerv1.SandboxCapability
}

func (s *stubComputerClient) HeartbeatComputer(_ context.Context, request *connect.Request[computerv1.HeartbeatComputerRequest]) (*connect.Response[computerv1.HeartbeatComputerResponse], error) {
	s.mu.Lock()
	s.heartbeats++
	count := s.heartbeats
	heartbeat := s.heartbeat
	s.capability = proto.Clone(request.Msg.GetSandboxCapability()).(*computerv1.SandboxCapability)
	s.mu.Unlock()
	if heartbeat != nil {
		if err := heartbeat(count); err != nil {
			return nil, err
		}
	}
	return connect.NewResponse(&computerv1.HeartbeatComputerResponse{}), nil
}

func (s *stubComputerClient) sandboxCapability() *computerv1.SandboxCapability {
	s.mu.Lock()
	defer s.mu.Unlock()
	return proto.Clone(s.capability).(*computerv1.SandboxCapability)
}

type stubPlacementClient struct {
	list func(context.Context) ([]*placementv1.AgentPlacement, error)
}

func (s *stubPlacementClient) ListComputerPlacements(ctx context.Context, _ *connect.Request[placementv1.ListComputerPlacementsRequest]) (*connect.Response[placementv1.ListComputerPlacementsResponse], error) {
	if s.list == nil {
		return connect.NewResponse(&placementv1.ListComputerPlacementsResponse{}), nil
	}
	placements, err := s.list(ctx)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&placementv1.ListComputerPlacementsResponse{Placements: placements}), nil
}

func (s *stubPlacementClient) AcknowledgeAgentPlacement(context.Context, *connect.Request[placementv1.AcknowledgeAgentPlacementRequest]) (*connect.Response[placementv1.AcknowledgeAgentPlacementResponse], error) {
	return nil, errors.New("unexpected acknowledgement")
}

type stubRuntimeClient struct {
	create func(*connect.Request[runtimev1.CreateAgentRuntimeSessionRequest]) (*runtimev1.AgentRuntimeSession, error)
	renew  func(*connect.Request[runtimev1.RenewAgentRuntimeSessionRequest]) (*runtimev1.AgentRuntimeSession, error)
}

func (s *stubRuntimeClient) CreateAgentRuntimeSession(_ context.Context, request *connect.Request[runtimev1.CreateAgentRuntimeSessionRequest]) (*connect.Response[runtimev1.CreateAgentRuntimeSessionResponse], error) {
	if s.create == nil {
		return nil, errors.New("unexpected runtime creation")
	}
	session, err := s.create(request)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&runtimev1.CreateAgentRuntimeSessionResponse{Session: session}), nil
}

func (s *stubRuntimeClient) RenewAgentRuntimeSession(_ context.Context, request *connect.Request[runtimev1.RenewAgentRuntimeSessionRequest]) (*connect.Response[runtimev1.RenewAgentRuntimeSessionResponse], error) {
	if s.renew == nil {
		return nil, errors.New("unexpected runtime renewal")
	}
	session, err := s.renew(request)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&runtimev1.RenewAgentRuntimeSessionResponse{Session: session}), nil
}

type stubInboxClient struct {
	mu       sync.Mutex
	observes int
	observe  func(*connect.Request[inboxv1.ObserveTargetRequest]) (*inboxv1.ObserveTargetResponse, error)
}

func (s *stubInboxClient) ObserveTarget(_ context.Context, request *connect.Request[inboxv1.ObserveTargetRequest]) (*connect.Response[inboxv1.ObserveTargetResponse], error) {
	s.mu.Lock()
	s.observes++
	observe := s.observe
	s.mu.Unlock()
	if observe != nil {
		response, err := observe(request)
		if err != nil {
			return nil, err
		}
		return connect.NewResponse(response), nil
	}
	spaceID := request.Msg.GetTarget().GetSpaceId()
	if spaceID == "" {
		return connect.NewResponse(&inboxv1.ObserveTargetResponse{}), nil
	}
	return connect.NewResponse(&inboxv1.ObserveTargetResponse{
		Target: request.Msg.GetTarget(), HeadSequence: 1,
		Messages: []*spacev1.Message{{Id: spaceID, SpaceId: spaceID, TargetSequence: 1, Body: "trigger"}},
	}), nil
}

func (s *stubInboxClient) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.observes
}

type stubDeliveryClient struct {
	mu       sync.Mutex
	lists    int
	accepts  int
	claims   int
	renews   int
	list     func(*connect.Request[deliveryv1.ListDeliveriesRequest]) (*deliveryv1.ListDeliveriesResponse, error)
	renew    func(*connect.Request[deliveryv1.RenewRunRequest]) (*deliveryv1.RunLaunch, error)
	complete func(context.Context, *connect.Request[deliveryv1.CompleteRunRequest]) (*deliveryv1.CompleteRunResponse, error)
	accept   func(*connect.Request[deliveryv1.AcceptDeliveryRequest]) (*deliveryv1.Run, error)
	claim    func(*connect.Request[deliveryv1.ClaimRunRequest]) (*deliveryv1.RunLaunch, error)
}

func (s *stubDeliveryClient) ListDeliveries(_ context.Context, request *connect.Request[deliveryv1.ListDeliveriesRequest]) (*connect.Response[deliveryv1.ListDeliveriesResponse], error) {
	s.mu.Lock()
	s.lists++
	s.mu.Unlock()
	if s.list == nil {
		return connect.NewResponse(&deliveryv1.ListDeliveriesResponse{}), nil
	}
	response, err := s.list(request)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(response), nil
}

func (s *stubDeliveryClient) AcceptDelivery(_ context.Context, request *connect.Request[deliveryv1.AcceptDeliveryRequest]) (*connect.Response[deliveryv1.AcceptDeliveryResponse], error) {
	s.mu.Lock()
	s.accepts++
	accept := s.accept
	s.mu.Unlock()
	if accept == nil {
		return nil, errors.New("unexpected accept")
	}
	run, err := accept(request)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&deliveryv1.AcceptDeliveryResponse{Run: run}), nil
}

func (s *stubDeliveryClient) ClaimRun(_ context.Context, request *connect.Request[deliveryv1.ClaimRunRequest]) (*connect.Response[deliveryv1.ClaimRunResponse], error) {
	s.mu.Lock()
	s.claims++
	claim := s.claim
	s.mu.Unlock()
	if claim == nil {
		return nil, errors.New("unexpected claim")
	}
	launch, err := claim(request)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&deliveryv1.ClaimRunResponse{Launch: launch}), nil
}

func (s *stubDeliveryClient) RenewRun(_ context.Context, request *connect.Request[deliveryv1.RenewRunRequest]) (*connect.Response[deliveryv1.RenewRunResponse], error) {
	if s.renew == nil {
		return nil, errors.New("unexpected renew")
	}
	launch, err := s.renew(request)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&deliveryv1.RenewRunResponse{Launch: launch}), nil
}

func (s *stubDeliveryClient) CompleteRun(ctx context.Context, request *connect.Request[deliveryv1.CompleteRunRequest]) (*connect.Response[deliveryv1.CompleteRunResponse], error) {
	if s.complete == nil {
		return nil, errors.New("unexpected complete")
	}
	response, err := s.complete(ctx, request)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(response), nil
}

func (s *stubDeliveryClient) incrementRenew() {
	s.mu.Lock()
	s.renews++
	s.mu.Unlock()
}

func (s *stubDeliveryClient) incrementList() {
	s.mu.Lock()
	s.lists++
	s.mu.Unlock()
}

func (s *stubDeliveryClient) listCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lists
}

func (s *stubDeliveryClient) acceptCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.accepts
}

func (s *stubDeliveryClient) claimCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.claims
}

func (s *stubDeliveryClient) renewCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.renews
}

type blockingExecutor struct{}

func (blockingExecutor) Execute(ctx context.Context, _ Execution) (Completion, error) {
	<-ctx.Done()
	return Completion{}, ctx.Err()
}

type eligibleTestExecutor struct {
	eligible bool
	err      error
}

func (e eligibleTestExecutor) Eligible(context.Context, string) (bool, error) {
	return e.eligible, e.err
}

func (eligibleTestExecutor) Execute(context.Context, Execution) (Completion, error) {
	return Completion{}, errors.New("unexpected execution")
}

type blockingCountingExecutor struct {
	started chan struct{}
	mu      sync.Mutex
	calls   int
	once    sync.Once
}

type executionCaptureExecutor struct {
	executions chan Execution
}

func newExecutionCaptureExecutor() *executionCaptureExecutor {
	return &executionCaptureExecutor{executions: make(chan Execution, 3)}
}

func (e *executionCaptureExecutor) Execute(_ context.Context, execution Execution) (Completion, error) {
	e.executions <- execution
	return Completion{}, errors.New("captured execution")
}

func newBlockingCountingExecutor() *blockingCountingExecutor {
	return &blockingCountingExecutor{started: make(chan struct{})}
}

func (e *blockingCountingExecutor) Execute(ctx context.Context, _ Execution) (Completion, error) {
	e.mu.Lock()
	e.calls++
	e.mu.Unlock()
	e.once.Do(func() { close(e.started) })
	<-ctx.Done()
	return Completion{}, ctx.Err()
}

func (e *blockingCountingExecutor) count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

type errorExecutor struct{}

func (errorExecutor) Execute(context.Context, Execution) (Completion, error) {
	return Completion{}, errors.New("executor failed")
}

type completionExecutor struct {
	completion Completion
}

func (e completionExecutor) Execute(context.Context, Execution) (Completion, error) {
	return e.completion, nil
}

type cancelCompletionExecutor struct{}

func (cancelCompletionExecutor) Execute(ctx context.Context, _ Execution) (Completion, error) {
	<-ctx.Done()
	return Completion{Outcome: deliveryv1.RunOutcome_RUN_OUTCOME_SUCCEEDED, Body: "must not persist"}, nil
}

type countingExecutor struct {
	mu    sync.Mutex
	calls int
}

func (e *countingExecutor) Execute(context.Context, Execution) (Completion, error) {
	e.mu.Lock()
	e.calls++
	e.mu.Unlock()
	return Completion{}, errors.New("unexpected execution")
}

func (e *countingExecutor) count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

func activePlacement(agentID, computerID string, generation uint64) *placementv1.AgentPlacement {
	return &placementv1.AgentPlacement{
		AgentId: agentID, ComputerId: computerID, Generation: generation,
		State: placementv1.PlacementState_PLACEMENT_STATE_ACTIVE,
	}
}

func activeDelivery(deliveryID, agentID string) *deliveryv1.Delivery {
	return &deliveryv1.Delivery{
		Id: deliveryID, AgentId: agentID, TriggerMessageId: agentID, SpaceId: agentID,
		Target: &spacev1.MessageTarget{Target: &spacev1.MessageTarget_SpaceId{SpaceId: agentID}}, TriggerTargetSequence: 1,
		State: deliveryv1.DeliveryState_DELIVERY_STATE_ACCEPTED,
	}
}

func availableDelivery(deliveryID, agentID string) *deliveryv1.Delivery {
	return &deliveryv1.Delivery{
		Id: deliveryID, AgentId: agentID, TriggerMessageId: agentID, SpaceId: agentID,
		Target: &spacev1.MessageTarget{Target: &spacev1.MessageTarget_SpaceId{SpaceId: agentID}}, TriggerTargetSequence: 1,
		State: deliveryv1.DeliveryState_DELIVERY_STATE_AVAILABLE,
	}
}

func activeRun(runID, deliveryID, agentID string) *deliveryv1.Run {
	return &deliveryv1.Run{
		Id: runID, DeliveryId: deliveryID, AgentId: agentID, BasisTargetSequence: 1,
		State: deliveryv1.RunState_RUN_STATE_RUNNING,
	}
}

func testTrigger(agentID string) triggerContext {
	return triggerContext{spaceID: agentID, observedHead: 1, body: "trigger"}
}

func canonicalCompleteResponse(request *deliveryv1.CompleteRunRequest, agentID string) *deliveryv1.CompleteRunResponse {
	now := timestamppb.Now()
	messageID := uuid.NewString()
	return &deliveryv1.CompleteRunResponse{
		Run: &deliveryv1.Run{
			Id: request.GetRunId(), AgentId: agentID, State: deliveryv1.RunState_RUN_STATE_COMPLETED,
			Outcome: request.GetOutcome(), ResultRef: &deliveryv1.Run_ResultMessageId{ResultMessageId: messageID}, CompletedAt: now,
		},
		Result:      &deliveryv1.CompleteRunResponse_Message{Message: &spacev1.Message{Id: messageID}},
		CommittedAt: now,
	}
}

func testLaunch(launchID, runID, agentID, computerID string, generation, fence uint64, expiresAt time.Time) *deliveryv1.RunLaunch {
	return &deliveryv1.RunLaunch{
		Id: launchID, RunId: runID, AgentId: agentID, HolderComputerId: computerID,
		HolderPlacementGeneration: generation, Fence: fence, ClaimedAt: timestamppb.New(expiresAt.Add(-time.Minute)),
		ExpiresAt: timestamppb.New(expiresAt),
	}
}

func reopenDaemonState(t *testing.T, fixture *daemonFixture) {
	t.Helper()
	if err := fixture.state.Close(); err != nil {
		t.Fatal(err)
	}
	state, err := computerstate.Open(fixture.dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	fixture.state = state
	fixture.daemon.config.State = state
}

func restartFixtureDaemon(fixture *daemonFixture) {
	daemon := NewDaemon(fixture.daemon.config)
	daemon.computers = fixture.computers
	daemon.placements = fixture.placements
	daemon.runtimes = fixture.runtimes
	daemon.inbox = fixture.inbox
	daemon.deliveries = fixture.deliveries
	fixture.daemon = daemon
}

func sameCompleteRequest(left, right *deliveryv1.CompleteRunRequest) bool {
	return proto.Equal(left, right)
}

func mutationStatus(attempts []computerstate.MutationAttempt, requestID string) computerstate.MutationStatus {
	for _, attempt := range attempts {
		if attempt.RequestID == requestID {
			return attempt.Status
		}
	}
	return ""
}

func bearer(value string) string {
	const prefix = "Bearer "
	if len(value) < len(prefix) || value[:len(prefix)] != prefix {
		return ""
	}
	return value[len(prefix):]
}

func testRuntimeToken(value byte) string {
	return base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{value}, 32))
}

func waitFor(t *testing.T, timeout time.Duration, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !ready() {
		if time.Now().After(deadline) {
			t.Fatal("condition did not become ready")
		}
		time.Sleep(time.Millisecond)
	}
}
