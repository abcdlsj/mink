package computerhost

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"path/filepath"
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
	"github.com/abcdlsj/sumi/internal/computerstate"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestDaemonSlowOutboxDoesNotStarveHeartbeatOrRunRenew(t *testing.T) {
	fixture := newDaemonFixture(t)
	defer fixture.state.Close()
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
	fixture.deliveries.renew = func(request *connect.Request[deliveryv1.RenewRunRequest]) (*deliveryv1.RunLaunch, error) {
		fixture.deliveries.incrementRenew()
		return &deliveryv1.RunLaunch{
			Id: request.Msg.GetLaunchId(), RunId: request.Msg.GetRunId(), AgentId: executionAgent,
			HolderComputerId: fixture.identity.ComputerID, HolderPlacementGeneration: 1,
			Fence: request.Msg.GetFence(), ExpiresAt: timestamppb.New(time.Now().Add(500 * time.Millisecond)),
		}, nil
	}
	completeStarted := make(chan struct{}, 1)
	fixture.deliveries.complete = func(ctx context.Context, request *connect.Request[deliveryv1.CompleteRunRequest]) (*deliveryv1.CompleteRunResponse, error) {
		select {
		case completeStarted <- struct{}{}:
		default:
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(150 * time.Millisecond):
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
	time.Sleep(100 * time.Millisecond)
	if heartbeats := fixture.computers.count(); heartbeats < 5 {
		t.Fatalf("heartbeats during slow Complete = %d, want >= 5", heartbeats)
	}
	if renews := fixture.deliveries.renewCount(); renews < 3 {
		t.Fatalf("Run renews during slow Complete = %d, want >= 3", renews)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
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
	if fixture.daemon.syncPlacements(context.Background(), fixture.identity) {
		t.Fatal("failed snapshot reported success")
	}
	if _, found, err := fixture.state.RuntimeSession(context.Background(), agentID); err != nil || !found {
		t.Fatalf("runtime after failed snapshot = %v, %v", found, err)
	}
	fixture.placements.list = func(context.Context) ([]*placementv1.AgentPlacement, error) { return nil, nil }
	if !fixture.daemon.syncPlacements(context.Background(), fixture.identity) {
		t.Fatal("complete empty snapshot reported failure")
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
		testLaunch(workerLaunchID, workerRunID, agentID, fixture.identity.ComputerID, 1, 1, now.Add(time.Minute)))
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
	if !fixture.daemon.syncPlacements(context.Background(), fixture.identity) {
		t.Fatal("generation rotation failed")
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
	if !fixture.daemon.syncPlacements(context.Background(), fixture.identity) {
		t.Fatal("same-binding remint after unauthenticated failed")
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
	if fixture.daemon.syncPlacements(context.Background(), fixture.identity) {
		t.Fatal("duplicate authoritative snapshot reported success")
	}
	unchanged, found, err := fixture.state.RuntimeSession(context.Background(), agentID)
	if err != nil || !found || unchanged.Token != session.Token || unchanged.PlacementGeneration != 2 || createCount != 2 {
		t.Fatalf("runtime changed after invalid snapshot = %+v, creates=%d, %v, %v", unchanged, createCount, found, err)
	}
}

func TestExecutorErrorAndCancelCreateNoOutbox(t *testing.T) {
	tests := []struct {
		name     string
		executor Executor
		cancel   bool
	}{
		{name: "technical error", executor: errorExecutor{}},
		{name: "shutdown cancel", executor: blockingExecutor{}, cancel: true},
		{name: "completion racing shutdown", executor: cancelCompletionExecutor{}, cancel: true},
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
			})
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
		testLaunch(launchID, runID, agentID, fixture.identity.ComputerID, 3, 9, now.Add(time.Minute)))
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
	fixture.computers.heartbeat = func(count int) error {
		mu.Lock()
		attempts = append(attempts, time.Now())
		mu.Unlock()
		if count <= 3 {
			return connect.NewError(connect.CodeUnavailable, errors.New("offline"))
		}
		return nil
	}
	fixture.placements.list = func(ctx context.Context) ([]*placementv1.AgentPlacement, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 130*time.Millisecond)
	defer cancel()
	if err := fixture.daemon.Run(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(attempts) < 7 || len(attempts) > 12 {
		t.Fatalf("heartbeat attempts = %d, want recovered bounded polling", len(attempts))
	}
	minimumGaps := []time.Duration{7 * time.Millisecond, 15 * time.Millisecond, 30 * time.Millisecond}
	maximumGaps := []time.Duration{12 * time.Millisecond, 19 * time.Millisecond, 38 * time.Millisecond}
	for index, minimum := range minimumGaps {
		gap := attempts[index+1].Sub(attempts[index])
		if gap < minimum || gap > maximumGaps[index] {
			t.Fatalf("heartbeat retry gap %d = %s, want [%s, %s]", index+1, gap, minimum, maximumGaps[index])
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
	if run := fixture.daemon.acceptDelivery(context.Background(), session, delivery); run != nil {
		t.Fatalf("first accept returned %+v", run)
	}
	reopenDaemonState(t, &fixture)
	if run := fixture.daemon.acceptDelivery(context.Background(), session, delivery); run == nil || run.GetId() != runID {
		t.Fatalf("replayed accept = %+v", run)
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
	if launch := fixture.daemon.claimRun(context.Background(), session, runID); launch != nil {
		t.Fatalf("first claim returned %+v", launch)
	}
	reopenDaemonState(t, &fixture)
	if launch := fixture.daemon.claimRun(context.Background(), session, runID); launch == nil || launch.GetId() != launchID {
		t.Fatalf("replayed claim = %+v", launch)
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
	if _, ok := fixture.daemon.renewRun(context.Background(), session, runID, launchID, 4, claimExpiry); ok {
		t.Fatal("first renew succeeded")
	}
	reopenDaemonState(t, &fixture)
	firstRenewExpiry, ok := fixture.daemon.renewRun(context.Background(), session, runID, launchID, 4, claimExpiry)
	if !ok {
		t.Fatal("replayed renew failed")
	}
	secondRenewExpiry, ok := fixture.daemon.renewRun(context.Background(), session, runID, launchID, 4, firstRenewExpiry)
	if !ok || !secondRenewExpiry.After(firstRenewExpiry) {
		t.Fatalf("next renew = %s, %v", secondRenewExpiry, ok)
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
	if run := fixture.daemon.acceptDelivery(context.Background(), session, availableDelivery(recoveryDeliveryID, agentID)); run != nil {
		t.Fatalf("unauthenticated accept returned %+v", run)
	}
	if run := fixture.daemon.acceptDelivery(context.Background(), session, availableDelivery(recoveryDeliveryID, agentID)); run == nil {
		t.Fatal("accept after runtime recovery failed")
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
		Operation: "run.claim", SubjectID: runID,
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
	if !fixture.daemon.dispatchAgent(context.Background(), fixture.identity, session) {
		t.Fatal("dispatch after restart failed")
	}
	if newClaimRequestID == "" || newClaimRequestID == claimAttempt.RequestID {
		t.Fatalf("new claim request id = %q, old = %q", newClaimRequestID, claimAttempt.RequestID)
	}
	for _, check := range []struct {
		operation string
		subject   string
		requestID string
	}{
		{"delivery.accept", deliveryID, acceptAttempt.RequestID},
		{"run.claim", runID, claimAttempt.RequestID},
		{"run.renew", oldLaunchID, renewAttempt.RequestID},
	} {
		attempts, err := fixture.state.MutationAttempts(context.Background(), check.operation, check.subject)
		if err != nil {
			t.Fatal(err)
		}
		if status := mutationStatus(attempts, check.requestID); status != "succeeded" {
			t.Fatalf("%s %s status = %q, attempts=%+v", check.operation, check.requestID, status, attempts)
		}
	}
	claimAttempts, err := fixture.state.MutationAttempts(context.Background(), "run.claim", runID)
	if err != nil || mutationStatus(claimAttempts, newClaimRequestID) != "succeeded" {
		t.Fatalf("new claim attempts = %+v, %v", claimAttempts, err)
	}
	fixture.daemon.stopAllWorkers()
	fixture.daemon.workersWG.Wait()
}

func TestDaemonRejectsImpossibleActiveRunLaunchCombinations(t *testing.T) {
	tests := []struct {
		name   string
		state  deliveryv1.RunState
		launch bool
	}{
		{name: "accepted with launch", state: deliveryv1.RunState_RUN_STATE_ACCEPTED, launch: true},
		{name: "running without launch", state: deliveryv1.RunState_RUN_STATE_RUNNING},
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
				}
				return response, nil
			}
			if fixture.daemon.dispatchAgent(context.Background(), fixture.identity, session) {
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
	if fixture.daemon.dispatchOutbox(context.Background()) {
		t.Fatal("lost response reported success")
	}
	reopenDaemonState(t, &fixture)
	if !fixture.daemon.dispatchOutbox(context.Background()) {
		t.Fatal("outbox replay failed")
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
	if launch := fixture.daemon.claimRun(context.Background(), session, runID); launch != nil {
		t.Fatalf("claim returned launch without durable receipt: %+v", launch)
	}
	state, err := computerstate.Open(fixture.dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	fixture.state = state
	fixture.daemon.config.State = state
	attempts, err := state.MutationAttempts(context.Background(), "run.claim", runID)
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
	if fixture.daemon.dispatchOutbox(context.Background()) {
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
	if !fixture.daemon.dispatchOutbox(context.Background()) {
		t.Fatal("terminal stale fence reported retryable failure")
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
}

func (s *stubComputerClient) HeartbeatComputer(context.Context, *connect.Request[computerv1.HeartbeatComputerRequest]) (*connect.Response[computerv1.HeartbeatComputerResponse], error) {
	s.mu.Lock()
	s.heartbeats++
	count := s.heartbeats
	heartbeat := s.heartbeat
	s.mu.Unlock()
	if heartbeat != nil {
		if err := heartbeat(count); err != nil {
			return nil, err
		}
	}
	return connect.NewResponse(&computerv1.HeartbeatComputerResponse{}), nil
}

func (s *stubComputerClient) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.heartbeats
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
}

func (s *stubInboxClient) ObserveTarget(context.Context, *connect.Request[inboxv1.ObserveTargetRequest]) (*connect.Response[inboxv1.ObserveTargetResponse], error) {
	s.mu.Lock()
	s.observes++
	s.mu.Unlock()
	return connect.NewResponse(&inboxv1.ObserveTargetResponse{}), nil
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
		Id: deliveryID, AgentId: agentID, Target: &spacev1.MessageTarget{},
		State: deliveryv1.DeliveryState_DELIVERY_STATE_ACCEPTED,
	}
}

func availableDelivery(deliveryID, agentID string) *deliveryv1.Delivery {
	return &deliveryv1.Delivery{
		Id: deliveryID, AgentId: agentID, Target: &spacev1.MessageTarget{},
		State: deliveryv1.DeliveryState_DELIVERY_STATE_AVAILABLE,
	}
}

func activeRun(runID, deliveryID, agentID string) *deliveryv1.Run {
	return &deliveryv1.Run{
		Id: runID, DeliveryId: deliveryID, AgentId: agentID,
		State: deliveryv1.RunState_RUN_STATE_RUNNING,
	}
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
		HolderPlacementGeneration: generation, Fence: fence, ExpiresAt: timestamppb.New(expiresAt),
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

func mutationStatus(attempts []computerstate.MutationAttempt, requestID string) string {
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
