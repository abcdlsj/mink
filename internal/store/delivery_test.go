package store

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestDeliveryAttentionCursorAndOneActiveRun(t *testing.T) {
	fixture := openDeliveryFixture(t)
	first := fixture.createTrigger(t, "first", 1)
	listed, err := fixture.database.ListDeliveries(context.Background(), ListDeliveriesParams{
		Authentication: fixture.authentication, Limit: 10, Now: fixture.at(2),
	})
	if err != nil || len(listed.Deliveries) != 1 || listed.Deliveries[0].TriggerMessageID != first.Message.ID {
		t.Fatalf("listed deliveries = %+v, %v", listed, err)
	}
	acceptRequest := uuid.NewString()
	if _, err := fixture.database.AcceptDelivery(context.Background(), AcceptDeliveryParams{
		RequestID: acceptRequest, Authentication: fixture.authentication,
		DeliveryID: first.Delivery.ID, Now: fixture.at(3),
	}); !errors.Is(err, ErrDeliveryCursorUnavailable) {
		t.Fatalf("accept without cursor error = %v", err)
	}
	assertRunWrites(t, fixture.database, acceptRequest, 0, 0)
	if item := readInboxItem(t, fixture.database, first.Item.ID); item.State != InboxStateUnread {
		t.Fatalf("item state after failed accept = %q", item.State)
	}
	observed, err := fixture.database.ObserveTarget(context.Background(), ObserveTargetParams{
		Authentication: AgentInboxAuthentication(fixture.authentication), Target: first.Item.Target, Limit: 20, Now: fixture.at(4),
	})
	if err != nil {
		t.Fatal(err)
	}
	run, err := fixture.database.AcceptDelivery(context.Background(), AcceptDeliveryParams{
		RequestID: acceptRequest, Authentication: fixture.authentication,
		DeliveryID: first.Delivery.ID, Now: fixture.at(5),
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.State != RunStateAccepted || run.BasisTargetSequence != observed.Head {
		t.Fatalf("accepted run = %+v", run)
	}
	assertRunAudit(t, fixture.database, acceptRequest, AuditRunAccept)
	if item := readInboxItem(t, fixture.database, first.Item.ID); item.State != InboxStateClaimed {
		t.Fatalf("accepted item state = %q", item.State)
	}
	second := fixture.createTrigger(t, "second", 6)
	if _, err := fixture.database.ObserveTarget(context.Background(), ObserveTargetParams{
		Authentication: AgentInboxAuthentication(fixture.authentication), Target: second.Item.Target, Limit: 20, Now: fixture.at(7),
	}); err != nil {
		t.Fatal(err)
	}
	listed, err = fixture.database.ListDeliveries(context.Background(), ListDeliveriesParams{
		Authentication: fixture.authentication, Limit: 10, Now: fixture.at(7),
	})
	if err != nil || listed.ActiveRun == nil || !reflect.DeepEqual(*listed.ActiveRun, run) ||
		listed.ActiveLaunch != nil || len(listed.Deliveries) != 1 || listed.Deliveries[0].ID != second.Delivery.ID {
		t.Fatalf("available queue with accepted run = %+v, %v", listed, err)
	}
	secondRequest := uuid.NewString()
	if _, err := fixture.database.AcceptDelivery(context.Background(), AcceptDeliveryParams{
		RequestID: secondRequest, Authentication: fixture.authentication,
		DeliveryID: second.Delivery.ID, Now: fixture.at(8),
	}); !errors.Is(err, ErrRunAlreadyActive) {
		t.Fatalf("second active run error = %v", err)
	}
	assertRunWrites(t, fixture.database, secondRequest, 1, 0)
	if delivery := readDelivery(t, fixture.database, second.Delivery.ID); delivery.State != DeliveryStateAvailable {
		t.Fatalf("second delivery state = %q", delivery.State)
	}
	replayed, err := fixture.database.AcceptDelivery(context.Background(), AcceptDeliveryParams{
		RequestID: acceptRequest, Authentication: fixture.authentication,
		DeliveryID: first.Delivery.ID, Now: fixture.at(9),
	})
	if err != nil || !reflect.DeepEqual(run, replayed) {
		t.Fatalf("accept replay = %+v, %v", replayed, err)
	}
	if _, err := fixture.database.ClaimRun(context.Background(), ClaimRunParams{
		RequestID: uuid.NewString(), Authentication: fixture.authentication, RunID: run.ID, Now: fixture.at(10),
	}); err != nil {
		t.Fatal(err)
	}
	replayed, err = fixture.database.AcceptDelivery(context.Background(), AcceptDeliveryParams{
		RequestID: acceptRequest, Authentication: fixture.authentication,
		DeliveryID: first.Delivery.ID, Now: fixture.at(11),
	})
	if err != nil || !reflect.DeepEqual(run, replayed) {
		t.Fatalf("accept replay after run start = %+v, %v", replayed, err)
	}
}

func TestRunLeaseFenceHolderAndReplacement(t *testing.T) {
	fixture := openDeliveryFixture(t)
	run := fixture.acceptTrigger(t, "lease", 1)
	claimRequest := uuid.NewString()
	first, err := fixture.database.ClaimRun(context.Background(), ClaimRunParams{
		RequestID: claimRequest, Authentication: fixture.authentication, RunID: run.ID, Now: fixture.at(5),
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Fence != 1 || first.HolderComputerID != fixture.authentication.Proof.ComputerID() ||
		first.HolderPlacementGeneration != fixture.authentication.Proof.PlacementGeneration() ||
		first.ExpiresAt.Sub(first.ClaimedAt) != runLeaseTTL {
		t.Fatalf("first launch = %+v", first)
	}
	assertRunAudit(t, fixture.database, claimRequest, AuditRunLaunch)
	replayed, err := fixture.database.ClaimRun(context.Background(), ClaimRunParams{
		RequestID: claimRequest, Authentication: fixture.authentication, RunID: run.ID, Now: fixture.at(6),
	})
	if err != nil || !reflect.DeepEqual(first, replayed) {
		t.Fatalf("claim replay = %+v, %v", replayed, err)
	}
	if _, err := fixture.database.ClaimRun(context.Background(), ClaimRunParams{
		RequestID: uuid.NewString(), Authentication: fixture.authentication, RunID: run.ID, Now: fixture.at(7),
	}); !errors.Is(err, ErrRunLaunchActive) {
		t.Fatalf("live claim error = %v", err)
	}
	renewRequest := uuid.NewString()
	renewed, err := fixture.database.RenewRun(context.Background(), RenewRunParams{
		RequestID: renewRequest, Authentication: fixture.authentication, RunID: run.ID,
		LaunchID: first.ID, Fence: first.Fence, Now: fixture.at(8),
	})
	if err != nil || !renewed.ExpiresAt.Equal(fixture.at(8).Add(runLeaseTTL)) {
		t.Fatalf("renewed launch = %+v, %v", renewed, err)
	}
	newToken := runtimeTestToken(180)
	_, err = fixture.database.CreateAgentRuntimeSession(context.Background(), CreateAgentRuntimeSessionParams{
		ComputerID: fixture.authentication.Proof.ComputerID(), RegistrationKey: "computer-registration-key",
		AgentID: fixture.agentID, PlacementGeneration: fixture.authentication.Proof.PlacementGeneration(),
		Token: newToken, Now: fixture.at(9), ExpiresAt: fixture.at(9).Add(agentRuntimeSessionTTL),
	})
	if err != nil {
		t.Fatal(err)
	}
	newAuthentication, err := fixture.database.AuthenticateAgentRuntimeSession(context.Background(), newToken, fixture.at(10))
	if err != nil {
		t.Fatal(err)
	}
	renewedAgain, err := fixture.database.RenewRun(context.Background(), RenewRunParams{
		RequestID: uuid.NewString(), Authentication: newAuthentication, RunID: run.ID,
		LaunchID: first.ID, Fence: first.Fence, Now: fixture.at(11),
	})
	if err != nil || renewedAgain.HolderComputerID != first.HolderComputerID {
		t.Fatalf("renew after token rotation = %+v, %v", renewedAgain, err)
	}
	fixture.authentication = newAuthentication
	second, err := fixture.database.ClaimRun(context.Background(), ClaimRunParams{
		RequestID: uuid.NewString(), Authentication: newAuthentication, RunID: run.ID,
		Now: renewedAgain.ExpiresAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.Fence != 2 || second.ID == first.ID || second.HolderComputerID != first.HolderComputerID {
		t.Fatalf("replacement launch = %+v", second)
	}
	claimReplay, err := fixture.database.ClaimRun(context.Background(), ClaimRunParams{
		RequestID: claimRequest, Authentication: newAuthentication, RunID: run.ID, Now: second.ClaimedAt.Add(time.Second),
	})
	if err != nil || !reflect.DeepEqual(first, claimReplay) {
		t.Fatalf("claim replay after replacement = %+v, %v", claimReplay, err)
	}
	renewReplay, err := fixture.database.RenewRun(context.Background(), RenewRunParams{
		RequestID: renewRequest, Authentication: newAuthentication, RunID: run.ID,
		LaunchID: first.ID, Fence: first.Fence, Now: second.ClaimedAt.Add(time.Second),
	})
	if err != nil || !reflect.DeepEqual(renewed, renewReplay) {
		t.Fatalf("renew replay after replacement = %+v, %v", renewReplay, err)
	}
	closed := readRunLaunch(t, fixture.database, first.ID)
	if closed.CloseReason != RunLaunchCloseReplaced || closed.ClosedAt == nil {
		t.Fatalf("closed launch = %+v", closed)
	}
	if _, err := fixture.database.RenewRun(context.Background(), RenewRunParams{
		RequestID: uuid.NewString(), Authentication: newAuthentication, RunID: run.ID,
		LaunchID: first.ID, Fence: first.Fence, Now: second.ClaimedAt.Add(time.Second),
	}); !errors.Is(err, ErrRunLaunchStale) {
		t.Fatalf("stale renew error = %v", err)
	}
}

func TestRunActiveDiscoverySurvivesRestartAndPlacementMigration(t *testing.T) {
	fixture := openDeliveryFixture(t)
	run := fixture.acceptTrigger(t, "discover active", 1)
	fixture.restart(t)
	accepted, err := fixture.database.ListDeliveries(context.Background(), ListDeliveriesParams{
		Authentication: fixture.authentication, Limit: 20, Now: fixture.at(4),
	})
	if err != nil || accepted.ActiveRun == nil || accepted.ActiveRun.ID != run.ID || accepted.ActiveLaunch != nil {
		t.Fatalf("accepted discovery after restart = %+v, %v", accepted, err)
	}
	first := fixture.claimRun(t, run, 5)
	fixture.restart(t)
	newAuthentication := migrateDeliveryRuntime(t, fixture, 2, 181, fixture.at(6))
	running, err := fixture.database.ListDeliveries(context.Background(), ListDeliveriesParams{
		Authentication: newAuthentication, Limit: 20, Now: fixture.at(9),
	})
	if err != nil || running.ActiveRun == nil || running.ActiveRun.ID != run.ID ||
		running.ActiveLaunch == nil || !reflect.DeepEqual(*running.ActiveLaunch, first) {
		t.Fatalf("running discovery after migration = %+v, %v", running, err)
	}
	if _, err := fixture.database.ClaimRun(context.Background(), ClaimRunParams{
		RequestID: uuid.NewString(), Authentication: newAuthentication, RunID: running.ActiveRun.ID, Now: fixture.at(10),
	}); !errors.Is(err, ErrRunLaunchActive) {
		t.Fatalf("migrated live claim error = %v", err)
	}
	replacement, err := fixture.database.ClaimRun(context.Background(), ClaimRunParams{
		RequestID: uuid.NewString(), Authentication: newAuthentication,
		RunID: running.ActiveRun.ID, Now: first.ExpiresAt,
	})
	if err != nil || replacement.Fence != first.Fence+1 ||
		replacement.HolderComputerID != newAuthentication.Proof.ComputerID() ||
		replacement.HolderPlacementGeneration != newAuthentication.Proof.PlacementGeneration() {
		t.Fatalf("migrated replacement = %+v, %v", replacement, err)
	}
}

func TestRunActiveDiscoveryRequiresCurrentAuthorization(t *testing.T) {
	t.Run("accepted", func(t *testing.T) {
		fixture := openDeliveryFixture(t)
		run := fixture.acceptTrigger(t, "accepted discovery access", 1)
		itemBefore := readInboxItemForDelivery(t, fixture.database, run.DeliveryID)
		deliveryBefore := readDelivery(t, fixture.database, run.DeliveryID)
		revokeDeliveryGrant(t, fixture, CapabilitySpaceRead, fixture.at(4))
		if _, err := fixture.database.ListDeliveries(context.Background(), ListDeliveriesParams{
			Authentication: fixture.authentication, Limit: 20, Now: fixture.at(5),
		}); !errors.Is(err, ErrInboxAccessLost) {
			t.Fatalf("accepted discovery after space read revoke error = %v", err)
		}
		itemAfter := readInboxItemForDelivery(t, fixture.database, run.DeliveryID)
		deliveryAfter := readDelivery(t, fixture.database, run.DeliveryID)
		runAfter, err := fixture.database.GetRun(context.Background(), GetRunParams{Authentication: fixture.authentication, RunID: run.ID, Now: fixture.at(6)})
		if err != nil || !reflect.DeepEqual(itemAfter, itemBefore) ||
			!reflect.DeepEqual(deliveryAfter, deliveryBefore) || !reflect.DeepEqual(runAfter, run) {
			t.Fatalf("accepted facts after rejected discovery = item:%+v delivery:%+v run:%+v error:%v", itemAfter, deliveryAfter, runAfter, err)
		}
		fixture.readGrant = fixture.issueAgentGrant(t, fixture.group.ID, CapabilitySpaceRead, fixture.at(7))
		listed, err := fixture.database.ListDeliveries(context.Background(), ListDeliveriesParams{
			Authentication: fixture.authentication, Limit: 20, Now: fixture.at(8),
		})
		if err != nil || listed.ActiveRun == nil || !reflect.DeepEqual(*listed.ActiveRun, run) || listed.ActiveLaunch != nil {
			t.Fatalf("accepted discovery after access restore = %+v, %v", listed, err)
		}
	})

	t.Run("running expired", func(t *testing.T) {
		fixture := openDeliveryFixture(t)
		run := fixture.acceptTrigger(t, "running discovery access", 1)
		launch := fixture.claimRun(t, run, 4)
		itemBefore := readInboxItemForDelivery(t, fixture.database, run.DeliveryID)
		deliveryBefore := readDelivery(t, fixture.database, run.DeliveryID)
		runBefore, err := fixture.database.GetRun(context.Background(), GetRunParams{Authentication: fixture.authentication, RunID: run.ID, Now: fixture.at(5)})
		if err != nil {
			t.Fatal(err)
		}
		launchBefore := readRunLaunch(t, fixture.database, launch.ID)
		revokeDeliveryGrant(t, fixture, CapabilitySpaceRead, fixture.at(6))
		if _, err := fixture.database.ListDeliveries(context.Background(), ListDeliveriesParams{
			Authentication: fixture.authentication, Limit: 20, Now: launch.ExpiresAt,
		}); !errors.Is(err, ErrInboxAccessLost) {
			t.Fatalf("expired discovery after space read revoke error = %v", err)
		}
		itemAfter := readInboxItemForDelivery(t, fixture.database, run.DeliveryID)
		deliveryAfter := readDelivery(t, fixture.database, run.DeliveryID)
		runAfter, err := fixture.database.GetRun(context.Background(), GetRunParams{Authentication: fixture.authentication, RunID: run.ID, Now: launch.ExpiresAt.Add(time.Second)})
		if err != nil {
			t.Fatal(err)
		}
		launchAfter := readRunLaunch(t, fixture.database, launch.ID)
		if !reflect.DeepEqual(itemAfter, itemBefore) || !reflect.DeepEqual(deliveryAfter, deliveryBefore) ||
			!reflect.DeepEqual(runAfter, runBefore) || !reflect.DeepEqual(launchAfter, launchBefore) {
			t.Fatalf("running facts after rejected discovery = item:%+v delivery:%+v run:%+v launch:%+v", itemAfter, deliveryAfter, runAfter, launchAfter)
		}
		fixture.readGrant = fixture.issueAgentGrant(t, fixture.group.ID, CapabilitySpaceRead, launch.ExpiresAt.Add(2*time.Second))
		listed, err := fixture.database.ListDeliveries(context.Background(), ListDeliveriesParams{
			Authentication: fixture.authentication, Limit: 20, Now: launch.ExpiresAt.Add(3 * time.Second),
		})
		if err != nil || listed.ActiveRun == nil || !reflect.DeepEqual(*listed.ActiveRun, runBefore) ||
			listed.ActiveLaunch == nil || !reflect.DeepEqual(*listed.ActiveLaunch, launchBefore) {
			t.Fatalf("expired discovery after access restore = %+v, %v", listed, err)
		}
	})
}

func TestRunReplayRequiresCurrentAuthorization(t *testing.T) {
	t.Run("accept space read", func(t *testing.T) {
		fixture := openDeliveryFixture(t)
		trigger := fixture.createTrigger(t, "accept authorization", 1)
		if _, err := fixture.database.ObserveTarget(context.Background(), ObserveTargetParams{
			Authentication: AgentInboxAuthentication(fixture.authentication), Target: trigger.Item.Target, Limit: 20, Now: fixture.at(2),
		}); err != nil {
			t.Fatal(err)
		}
		requestID := uuid.NewString()
		run, err := fixture.database.AcceptDelivery(context.Background(), AcceptDeliveryParams{
			RequestID: requestID, Authentication: fixture.authentication,
			DeliveryID: trigger.Delivery.ID, Now: fixture.at(3),
		})
		if err != nil {
			t.Fatal(err)
		}
		revokeDeliveryGrant(t, fixture, CapabilitySpaceRead, fixture.at(4))
		itemBefore := readInboxItem(t, fixture.database, trigger.Item.ID)
		deliveryBefore := readDelivery(t, fixture.database, trigger.Delivery.ID)
		if _, err := fixture.database.AcceptDelivery(context.Background(), AcceptDeliveryParams{
			RequestID: requestID, Authentication: fixture.authentication,
			DeliveryID: trigger.Delivery.ID, Now: fixture.at(5),
		}); !errors.Is(err, ErrInboxAccessLost) {
			t.Fatalf("accept replay after space read revoke error = %v", err)
		}
		itemAfter := readInboxItem(t, fixture.database, trigger.Item.ID)
		deliveryAfter := readDelivery(t, fixture.database, trigger.Delivery.ID)
		current, err := fixture.database.GetRun(context.Background(), GetRunParams{Authentication: fixture.authentication, RunID: run.ID, Now: fixture.at(6)})
		if err != nil || !reflect.DeepEqual(itemAfter, itemBefore) || !reflect.DeepEqual(deliveryAfter, deliveryBefore) || !reflect.DeepEqual(current, run) {
			t.Fatalf("facts after rejected accept replay = item:%+v delivery:%+v run:%+v error:%v", itemAfter, deliveryAfter, current, err)
		}
		assertRunAudit(t, fixture.database, requestID, AuditRunAccept)
		newRequestID := uuid.NewString()
		if _, err := fixture.database.AcceptDelivery(context.Background(), AcceptDeliveryParams{
			RequestID: newRequestID, Authentication: fixture.authentication,
			DeliveryID: trigger.Delivery.ID, Now: fixture.at(7),
		}); !errors.Is(err, ErrInboxAccessLost) {
			t.Fatalf("new accept after space read revoke error = %v", err)
		}
		assertRunWrites(t, fixture.database, newRequestID, 1, 0)
		if item := readInboxItem(t, fixture.database, trigger.Item.ID); !reflect.DeepEqual(item, itemBefore) {
			t.Fatalf("item after rejected new accept = %+v", item)
		}
		fixture.readGrant = fixture.issueAgentGrant(t, fixture.group.ID, CapabilitySpaceRead, fixture.at(8))
		replayed, err := fixture.database.AcceptDelivery(context.Background(), AcceptDeliveryParams{
			RequestID: requestID, Authentication: fixture.authentication,
			DeliveryID: trigger.Delivery.ID, Now: fixture.at(9),
		})
		if err != nil || !reflect.DeepEqual(replayed, run) {
			t.Fatalf("accept replay after space read restore = %+v, %v", replayed, err)
		}
	})

	t.Run("complete message grant", func(t *testing.T) {
		fixture := openDeliveryFixture(t)
		run := fixture.acceptTrigger(t, "complete authorization", 1)
		launch := fixture.claimRun(t, run, 4)
		params := CompleteRunParams{
			RequestID: uuid.NewString(), OutboxEventID: uuid.NewString(), Authentication: fixture.authentication,
			RunID: run.ID, LaunchID: launch.ID, Fence: launch.Fence,
			Outcome: RunOutcomeSucceeded, Body: "authorized completion", Now: fixture.at(5),
		}
		completed, err := fixture.database.CompleteRun(context.Background(), params)
		if err != nil {
			t.Fatal(err)
		}
		revokeDeliveryGrant(t, fixture, CapabilityMessageSend, fixture.at(6))
		params.Now = fixture.at(7)
		if _, err := fixture.database.CompleteRun(context.Background(), params); !errors.Is(err, ErrPermissionDenied) {
			t.Fatalf("completion replay after message grant revoke error = %v", err)
		}
		assertRunCompletionCounts(t, fixture.database, run.ID, params.RequestID, 1, 1, 1)
		assertRunAudit(t, fixture.database, params.RequestID, AuditRunComplete)
		fixture.issueAgentGrant(t, fixture.group.ID, CapabilityMessageSend, fixture.at(8))
		params.Now = fixture.at(9)
		replayed, err := fixture.database.CompleteRun(context.Background(), params)
		if err != nil || !reflect.DeepEqual(replayed, completed) {
			t.Fatalf("completion replay after message grant restore = %+v, %v", replayed, err)
		}
	})

	t.Run("held completion space read", func(t *testing.T) {
		fixture := openDeliveryFixture(t)
		run := fixture.acceptTrigger(t, "held authorization", 1)
		launch := fixture.claimRun(t, run, 4)
		if _, err := fixture.database.SendMessage(context.Background(), SendMessageParams{
			RequestID: uuid.NewString(), Actor: fixture.owner,
			Target: MessageTarget{Kind: MessageTargetSpace, ID: fixture.group.ID},
			Body:   "advance held authorization", Now: fixture.at(5),
		}); err != nil {
			t.Fatal(err)
		}
		params := CompleteRunParams{
			RequestID: uuid.NewString(), OutboxEventID: uuid.NewString(), Authentication: fixture.authentication,
			RunID: run.ID, LaunchID: launch.ID, Fence: launch.Fence,
			Outcome: RunOutcomeSucceeded, Body: "held authorized completion", Now: fixture.at(6),
		}
		completed, err := fixture.database.CompleteRun(context.Background(), params)
		if err != nil || completed.HeldDraft == nil {
			t.Fatalf("held completion = %+v, %v", completed, err)
		}
		revokeDeliveryGrant(t, fixture, CapabilitySpaceRead, fixture.at(7))
		itemBefore := readInboxItemForDelivery(t, fixture.database, run.DeliveryID)
		deliveryBefore := readDelivery(t, fixture.database, run.DeliveryID)
		runBefore, err := fixture.database.GetRun(context.Background(), GetRunParams{Authentication: fixture.authentication, RunID: run.ID, Now: fixture.at(8)})
		if err != nil {
			t.Fatal(err)
		}
		launchBefore := readRunLaunch(t, fixture.database, launch.ID)
		draftBefore, err := scanHeldDraft(fixture.database.db.QueryRow(heldDraftSelect+` WHERE id = ?`, completed.HeldDraft.ID))
		if err != nil {
			t.Fatal(err)
		}
		params.Now = fixture.at(9)
		if _, err := fixture.database.CompleteRun(context.Background(), params); !errors.Is(err, ErrInboxAccessLost) {
			t.Fatalf("held completion replay after space read revoke error = %v", err)
		}
		itemAfter := readInboxItemForDelivery(t, fixture.database, run.DeliveryID)
		deliveryAfter := readDelivery(t, fixture.database, run.DeliveryID)
		runAfter, err := fixture.database.GetRun(context.Background(), GetRunParams{Authentication: fixture.authentication, RunID: run.ID, Now: fixture.at(10)})
		if err != nil {
			t.Fatal(err)
		}
		launchAfter := readRunLaunch(t, fixture.database, launch.ID)
		draftAfter, err := scanHeldDraft(fixture.database.db.QueryRow(heldDraftSelect+` WHERE id = ?`, completed.HeldDraft.ID))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(itemAfter, itemBefore) || !reflect.DeepEqual(deliveryAfter, deliveryBefore) ||
			!reflect.DeepEqual(runAfter, runBefore) || !reflect.DeepEqual(launchAfter, launchBefore) ||
			!reflect.DeepEqual(draftAfter, draftBefore) {
			t.Fatalf("held facts changed after rejected replay: item:%+v delivery:%+v run:%+v launch:%+v draft:%+v", itemAfter, deliveryAfter, runAfter, launchAfter, draftAfter)
		}
		assertRunCompletionCounts(t, fixture.database, run.ID, params.RequestID, 0, 1, 1)
		assertRunAudit(t, fixture.database, params.RequestID, AuditRunComplete)
		fixture.readGrant = fixture.issueAgentGrant(t, fixture.group.ID, CapabilitySpaceRead, fixture.at(11))
		params.Now = fixture.at(12)
		replayed, err := fixture.database.CompleteRun(context.Background(), params)
		if err != nil || !reflect.DeepEqual(replayed, completed) {
			t.Fatalf("held completion replay after space read restore = %+v, %v", replayed, err)
		}
	})
}

func TestRunReplayRequiresCurrentHolderAndAllowsTokenRotation(t *testing.T) {
	t.Run("launch", func(t *testing.T) {
		fixture := openDeliveryFixture(t)
		run := fixture.acceptTrigger(t, "launch holder", 1)
		requestID := uuid.NewString()
		launch, err := fixture.database.ClaimRun(context.Background(), ClaimRunParams{
			RequestID: requestID, Authentication: fixture.authentication, RunID: run.ID, Now: fixture.at(4),
		})
		if err != nil {
			t.Fatal(err)
		}
		renewRequestID := uuid.NewString()
		renewed, err := fixture.database.RenewRun(context.Background(), RenewRunParams{
			RequestID: renewRequestID, Authentication: fixture.authentication,
			RunID: run.ID, LaunchID: launch.ID, Fence: launch.Fence, Now: fixture.at(5),
		})
		if err != nil {
			t.Fatal(err)
		}
		rotated := rotateDeliveryRuntime(t, fixture, 182, fixture.at(6))
		replayed, err := fixture.database.ClaimRun(context.Background(), ClaimRunParams{
			RequestID: requestID, Authentication: rotated, RunID: run.ID, Now: fixture.at(8),
		})
		if err != nil || !reflect.DeepEqual(replayed, launch) {
			t.Fatalf("claim replay after token rotation = %+v, %v", replayed, err)
		}
		renewReplay, err := fixture.database.RenewRun(context.Background(), RenewRunParams{
			RequestID: renewRequestID, Authentication: rotated,
			RunID: run.ID, LaunchID: launch.ID, Fence: launch.Fence, Now: fixture.at(8),
		})
		if err != nil || !reflect.DeepEqual(renewReplay, renewed) {
			t.Fatalf("renew replay after token rotation = %+v, %v", renewReplay, err)
		}
		migrated := migrateDeliveryRuntime(t, fixture, 2, 183, fixture.at(9))
		if _, err := fixture.database.ClaimRun(context.Background(), ClaimRunParams{
			RequestID: requestID, Authentication: migrated, RunID: run.ID, Now: fixture.at(12),
		}); !errors.Is(err, ErrRunLaunchStale) {
			t.Fatalf("claim replay after placement migration error = %v", err)
		}
		if _, err := fixture.database.RenewRun(context.Background(), RenewRunParams{
			RequestID: renewRequestID, Authentication: migrated,
			RunID: run.ID, LaunchID: launch.ID, Fence: launch.Fence, Now: fixture.at(12),
		}); !errors.Is(err, ErrRunLaunchStale) {
			t.Fatalf("renew replay after placement migration error = %v", err)
		}
		assertRunAudit(t, fixture.database, requestID, AuditRunLaunch)
	})

	t.Run("completion", func(t *testing.T) {
		fixture := openDeliveryFixture(t)
		run := fixture.acceptTrigger(t, "completion holder", 1)
		launch := fixture.claimRun(t, run, 4)
		params := CompleteRunParams{
			RequestID: uuid.NewString(), OutboxEventID: uuid.NewString(), Authentication: fixture.authentication,
			RunID: run.ID, LaunchID: launch.ID, Fence: launch.Fence,
			Outcome: RunOutcomeSucceeded, Body: "holder completion", Now: fixture.at(5),
		}
		completed, err := fixture.database.CompleteRun(context.Background(), params)
		if err != nil {
			t.Fatal(err)
		}
		params.Authentication = rotateDeliveryRuntime(t, fixture, 184, fixture.at(6))
		params.Now = fixture.at(8)
		replayed, err := fixture.database.CompleteRun(context.Background(), params)
		if err != nil || !reflect.DeepEqual(replayed, completed) {
			t.Fatalf("completion replay after token rotation = %+v, %v", replayed, err)
		}
		params.Authentication = migrateDeliveryRuntime(t, fixture, 2, 185, fixture.at(9))
		params.Now = fixture.at(12)
		if _, err := fixture.database.CompleteRun(context.Background(), params); !errors.Is(err, ErrRunLaunchStale) {
			t.Fatalf("completion replay after placement migration error = %v", err)
		}
		assertRunCompletionCounts(t, fixture.database, run.ID, params.RequestID, 1, 1, 1)
		assertRunAudit(t, fixture.database, params.RequestID, AuditRunComplete)
	})
}

func TestRunCompleteFreshReplayAndGlobalConflicts(t *testing.T) {
	fixture := openDeliveryFixture(t)
	mentionID := fixture.createMentionMember(t, "result-peer", fixture.at(1))
	run := fixture.acceptTrigger(t, "fresh", 3)
	launch := fixture.claimRun(t, run, 7)
	params := CompleteRunParams{
		RequestID: uuid.NewString(), OutboxEventID: uuid.NewString(),
		Authentication: fixture.authentication, RunID: run.ID, LaunchID: launch.ID, Fence: launch.Fence,
		Outcome: RunOutcomeSucceeded, Body: "unique completed result body",
		MentionedPrincipals: agentPrincipals(fixture.owner.OrganizationID, mentionID), Now: fixture.at(8),
	}
	completed, err := fixture.database.CompleteRun(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Kind != InboxResultMessage || completed.Message == nil || completed.Run.State != RunStateCompleted ||
		completed.Run.Outcome != RunOutcomeSucceeded || completed.Run.ResultID != completed.Message.ID {
		t.Fatalf("completed run = %+v", completed)
	}
	if item := readInboxItemForDelivery(t, fixture.database, run.DeliveryID); item.Completion != InboxCompletionSent {
		t.Fatalf("completed inbox item = %+v", item)
	}
	if delivery := readDelivery(t, fixture.database, run.DeliveryID); delivery.State != DeliveryStateCompleted {
		t.Fatalf("completed delivery = %+v", delivery)
	}
	closed := readRunLaunch(t, fixture.database, launch.ID)
	if closed.CloseReason != RunLaunchCloseCompleted || closed.ClosedAt == nil {
		t.Fatalf("completed launch = %+v", closed)
	}
	replayed, err := fixture.database.CompleteRun(context.Background(), CompleteRunParams{
		RequestID: params.RequestID, OutboxEventID: params.OutboxEventID,
		Authentication: fixture.authentication, RunID: params.RunID, LaunchID: params.LaunchID,
		Fence: params.Fence, Outcome: params.Outcome, Body: params.Body,
		MentionedPrincipals: params.MentionedPrincipals, Now: fixture.at(9),
	})
	if err != nil || !reflect.DeepEqual(completed, replayed) {
		t.Fatalf("completion replay = %+v, %v", replayed, err)
	}
	assertRunAudit(t, fixture.database, params.RequestID, AuditRunComplete)
	conflicts := []CompleteRunParams{
		{RequestID: uuid.NewString(), OutboxEventID: params.OutboxEventID},
		{RequestID: params.RequestID, OutboxEventID: uuid.NewString()},
		{RequestID: uuid.NewString(), OutboxEventID: uuid.NewString()},
	}
	for _, conflict := range conflicts {
		conflict.Authentication = fixture.authentication
		conflict.RunID = run.ID
		conflict.LaunchID = launch.ID
		conflict.Fence = launch.Fence
		conflict.Outcome = RunOutcomeSucceeded
		conflict.Body = params.Body + " conflict"
		conflict.Now = fixture.at(10)
		if _, err := fixture.database.CompleteRun(context.Background(), conflict); !errors.Is(err, ErrRunCompletionConflict) && !errors.Is(err, ErrInboxRequestConflict) {
			t.Fatalf("completion conflict error = %v", err)
		}
	}
	var snapshot string
	if err := fixture.database.db.QueryRow(`SELECT CAST(response_snapshot AS TEXT) FROM inbox_requests WHERE request_id = ?`, params.RequestID).Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(snapshot, params.Body) || strings.Contains(snapshot, mentionID) {
		t.Fatalf("completion receipt leaked content: %s", snapshot)
	}
	assertRunCompletionCounts(t, fixture.database, run.ID, params.RequestID, 1, 1, 1)
}

func TestRunCompleteHeldReplayAfterDraftResolution(t *testing.T) {
	fixture := openDeliveryFixture(t)
	run := fixture.acceptTrigger(t, "held", 1)
	launch := fixture.claimRun(t, run, 5)
	if _, err := fixture.database.SendMessage(context.Background(), SendMessageParams{
		RequestID: uuid.NewString(), Actor: fixture.owner,
		Target: MessageTarget{Kind: MessageTargetSpace, ID: fixture.group.ID},
		Body:   "advance while running", Now: fixture.at(6),
	}); err != nil {
		t.Fatal(err)
	}
	params := CompleteRunParams{
		RequestID: uuid.NewString(), OutboxEventID: uuid.NewString(),
		Authentication: fixture.authentication, RunID: run.ID, LaunchID: launch.ID, Fence: launch.Fence,
		Outcome: RunOutcomeSucceeded, Body: "held run result", Now: fixture.at(7),
	}
	held, err := fixture.database.CompleteRun(context.Background(), params)
	if err != nil || held.Kind != InboxResultHeldDraft || held.HeldDraft == nil {
		t.Fatalf("held completion = %+v, %v", held, err)
	}
	if item := readInboxItemForDelivery(t, fixture.database, run.DeliveryID); item.State != InboxStateClaimed {
		t.Fatalf("held completion item = %+v", item)
	}
	observed, err := fixture.database.ObserveTarget(context.Background(), ObserveTargetParams{
		Authentication: AgentInboxAuthentication(fixture.authentication), Target: held.HeldDraft.Target, Limit: 20, Now: fixture.at(8),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.ResolveHeldDraft(context.Background(), ResolveHeldDraftParams{
		RequestID: uuid.NewString(), Authentication: fixture.authentication,
		HeldDraftID: held.HeldDraft.ID, Action: DraftResolutionRetry,
		BasisTargetSequence: observed.Head, Now: fixture.at(9),
	}); err != nil {
		t.Fatal(err)
	}
	replayed, err := fixture.database.CompleteRun(context.Background(), CompleteRunParams{
		RequestID: params.RequestID, OutboxEventID: params.OutboxEventID,
		Authentication: fixture.authentication, RunID: params.RunID, LaunchID: params.LaunchID,
		Fence: params.Fence, Outcome: params.Outcome, Body: params.Body, Now: fixture.at(10),
	})
	if err != nil || !reflect.DeepEqual(held, replayed) {
		t.Fatalf("held completion replay = %+v, %v", replayed, err)
	}
	listed, err := fixture.database.ListDeliveries(context.Background(), ListDeliveriesParams{
		Authentication: fixture.authentication, Limit: 20, Now: fixture.at(11),
	})
	if err != nil || len(listed.Deliveries) != 0 {
		t.Fatalf("deliveries after held completion = %+v, %v", listed, err)
	}
	var messages int
	if err := fixture.database.db.QueryRow(`SELECT count(*) FROM messages WHERE request_id = ?`, params.RequestID).Scan(&messages); err != nil || messages != 0 {
		t.Fatalf("held completion messages = %d, %v", messages, err)
	}
}

func TestRunStaleLaunchCannotComplete(t *testing.T) {
	fixture := openDeliveryFixture(t)
	run := fixture.acceptTrigger(t, "stale", 1)
	first := fixture.claimRun(t, run, 5)
	second, err := fixture.database.ClaimRun(context.Background(), ClaimRunParams{
		RequestID: uuid.NewString(), Authentication: fixture.authentication,
		RunID: run.ID, Now: first.ExpiresAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	requestID := uuid.NewString()
	if _, err := fixture.database.CompleteRun(context.Background(), CompleteRunParams{
		RequestID: requestID, OutboxEventID: uuid.NewString(), Authentication: fixture.authentication,
		RunID: run.ID, LaunchID: first.ID, Fence: first.Fence,
		Outcome: RunOutcomeSucceeded, Body: "stale result", Now: second.ClaimedAt.Add(time.Second),
	}); !errors.Is(err, ErrRunLaunchStale) {
		t.Fatalf("stale completion error = %v", err)
	}
	assertRunWrites(t, fixture.database, requestID, 1, 2)
	current, err := fixture.database.GetRun(context.Background(), GetRunParams{Authentication: fixture.authentication, RunID: run.ID, Now: second.ClaimedAt.Add(2 * time.Second)})
	if err != nil || current.State != RunStateRunning || current.ResultID != "" {
		t.Fatalf("run after stale completion = %+v, %v", current, err)
	}
}

func TestDeliveryCurrentGrantFailClosed(t *testing.T) {
	fixture := openInboxFixture(t)
	trigger := createDeliveryTrigger(t, fixture, "grant", 1)
	if _, err := fixture.database.ObserveTarget(context.Background(), ObserveTargetParams{
		Authentication: AgentInboxAuthentication(fixture.authentication), Target: trigger.Item.Target, Limit: 20, Now: fixture.at(2),
	}); err != nil {
		t.Fatal(err)
	}
	requestID := uuid.NewString()
	if _, err := fixture.database.AcceptDelivery(context.Background(), AcceptDeliveryParams{
		RequestID: requestID, Authentication: fixture.authentication,
		DeliveryID: trigger.Delivery.ID, Now: fixture.at(3),
	}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("accept without run grant error = %v", err)
	}
	assertRunWrites(t, fixture.database, requestID, 0, 0)
	issueRunGrant(t, fixture, fixture.at(4))
	run, err := fixture.database.AcceptDelivery(context.Background(), AcceptDeliveryParams{
		RequestID: requestID, Authentication: fixture.authentication,
		DeliveryID: trigger.Delivery.ID, Now: fixture.at(5),
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.State != RunStateAccepted {
		t.Fatalf("accepted run = %+v", run)
	}
}

type deliveryFixture struct {
	*inboxFixture
	path string
}

type deliveryTrigger struct {
	Message  Message
	Item     InboxItem
	Delivery Delivery
}

func openDeliveryFixture(t *testing.T) *deliveryFixture {
	t.Helper()
	fixture := openInboxFixture(t)
	issueRunGrant(t, fixture, fixture.at(0))
	var sequence int
	var name, path string
	if err := fixture.database.db.QueryRow(`PRAGMA database_list`).Scan(&sequence, &name, &path); err != nil {
		t.Fatal(err)
	}
	return &deliveryFixture{inboxFixture: fixture, path: path}
}

func (f *deliveryFixture) restart(t *testing.T) {
	t.Helper()
	if err := f.database.Close(); err != nil {
		t.Fatal(err)
	}
	database, err := Open(f.path)
	if err != nil {
		t.Fatal(err)
	}
	f.database = database
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Error(err)
		}
	})
}

func rotateDeliveryRuntime(t *testing.T, fixture *deliveryFixture, tokenValue byte, now time.Time) AgentRuntimeAuthentication {
	t.Helper()
	token := runtimeTestToken(tokenValue)
	if _, err := fixture.database.CreateAgentRuntimeSession(context.Background(), CreateAgentRuntimeSessionParams{
		ComputerID: fixture.authentication.Proof.ComputerID(), RegistrationKey: "computer-registration-key",
		AgentID: fixture.agentID, PlacementGeneration: fixture.authentication.Proof.PlacementGeneration(),
		Token: token, Now: now, ExpiresAt: now.Add(agentRuntimeSessionTTL),
	}); err != nil {
		t.Fatal(err)
	}
	authentication, err := fixture.database.AuthenticateAgentRuntimeSession(context.Background(), token, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	fixture.authentication = authentication
	return authentication
}

func migrateDeliveryRuntime(t *testing.T, fixture *deliveryFixture, generation uint64, tokenValue byte, now time.Time) AgentRuntimeAuthentication {
	t.Helper()
	registrationKey := "migrated-computer-registration-key-" + uuid.NewString()
	computer, err := fixture.database.RegisterComputer(context.Background(), RegisterComputerParams{
		RegistrationKey: registrationKey, Name: "migrated-runtime", OS: "linux", Arch: "arm64", Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.db.Exec(`
		UPDATE agent_placements
		SET computer_id = ?, generation = ?, state = 'active', error_code = '', updated_at = ?
		WHERE agent_id = ?
	`, computer.ID, generation, unixNano(now), fixture.agentID); err != nil {
		t.Fatal(err)
	}
	token := runtimeTestToken(tokenValue)
	if _, err := fixture.database.CreateAgentRuntimeSession(context.Background(), CreateAgentRuntimeSessionParams{
		ComputerID: computer.ID, RegistrationKey: registrationKey, AgentID: fixture.agentID,
		PlacementGeneration: generation, Token: token, Now: now.Add(time.Second),
		ExpiresAt: now.Add(time.Second).Add(agentRuntimeSessionTTL),
	}); err != nil {
		t.Fatal(err)
	}
	authentication, err := fixture.database.AuthenticateAgentRuntimeSession(context.Background(), token, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	fixture.authentication = authentication
	return authentication
}

func revokeDeliveryGrant(t *testing.T, fixture *deliveryFixture, capability Capability, now time.Time) Grant {
	t.Helper()
	grant, err := scanGrant(fixture.database.db.QueryRow(grantSelect+`
		WHERE subject_kind = 'agent' AND subject_id = ? AND capability = ?
		  AND scope_kind = 'space' AND scope_id = ? AND revoked_at IS NULL
	`, fixture.agentID, capability, fixture.group.ID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.RevokeGrant(context.Background(), RevokeGrantParams{
		RequestID: uuid.NewString(), Actor: fixture.owner, GrantID: grant.ID, Now: now,
	}); err != nil {
		t.Fatal(err)
	}
	return grant
}

func issueRunGrant(t *testing.T, fixture *inboxFixture, now time.Time) Grant {
	t.Helper()
	grant, err := fixture.database.IssueGrant(context.Background(), IssueGrantParams{
		RequestID: uuid.NewString(), Actor: fixture.owner,
		Subject:    Principal{Kind: "agent", ID: fixture.agentID, OrganizationID: fixture.owner.OrganizationID},
		Capability: CapabilityRunExecute, Scope: Scope{Kind: "agent", ID: fixture.agentID},
		ParentGrantID: fixture.rootGrant.ID, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return grant
}

func (f *deliveryFixture) createTrigger(t *testing.T, body string, second int) deliveryTrigger {
	t.Helper()
	return createDeliveryTrigger(t, f.inboxFixture, body, second)
}

func createDeliveryTrigger(t *testing.T, fixture *inboxFixture, body string, second int) deliveryTrigger {
	t.Helper()
	message, err := fixture.database.SendMessage(context.Background(), SendMessageParams{
		RequestID: uuid.NewString(), Actor: fixture.owner,
		Target: MessageTarget{Kind: MessageTargetSpace, ID: fixture.group.ID}, Body: body,
		MentionedPrincipals: agentPrincipals(fixture.owner.OrganizationID, fixture.agentID), Now: fixture.at(second),
	})
	if err != nil {
		t.Fatal(err)
	}
	item, err := scanInboxItem(fixture.database.db.QueryRow(inboxItemSelect+` WHERE trigger_message_id = ? AND recipient_kind = 'agent' AND recipient_id = ?`, message.ID, fixture.agentID))
	if err != nil {
		t.Fatal(err)
	}
	delivery := readDeliveryByMessage(t, fixture.database, fixture.agentID, message.ID)
	return deliveryTrigger{Message: message, Item: item, Delivery: delivery}
}

func (f *deliveryFixture) acceptTrigger(t *testing.T, body string, second int) Run {
	t.Helper()
	trigger := f.createTrigger(t, body, second)
	if _, err := f.database.ObserveTarget(context.Background(), ObserveTargetParams{
		Authentication: AgentInboxAuthentication(f.authentication), Target: trigger.Item.Target, Limit: 200, Now: f.at(second + 1),
	}); err != nil {
		t.Fatal(err)
	}
	run, err := f.database.AcceptDelivery(context.Background(), AcceptDeliveryParams{
		RequestID: uuid.NewString(), Authentication: f.authentication,
		DeliveryID: trigger.Delivery.ID, Now: f.at(second + 2),
	})
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func (f *deliveryFixture) claimRun(t *testing.T, run Run, second int) RunLaunch {
	t.Helper()
	launch, err := f.database.ClaimRun(context.Background(), ClaimRunParams{
		RequestID: uuid.NewString(), Authentication: f.authentication, RunID: run.ID, Now: f.at(second),
	})
	if err != nil {
		t.Fatal(err)
	}
	return launch
}

func readDelivery(t *testing.T, database *Store, deliveryID string) Delivery {
	t.Helper()
	delivery, err := scanDelivery(database.db.QueryRow(deliverySelect+` WHERE deliveries.id = ?`, deliveryID))
	if err != nil {
		t.Fatal(err)
	}
	return delivery
}

func readDeliveryByMessage(t *testing.T, database *Store, agentID, messageID string) Delivery {
	t.Helper()
	delivery, err := scanDelivery(database.db.QueryRow(deliverySelect+` WHERE deliveries.agent_id = ? AND deliveries.trigger_message_id = ?`, agentID, messageID))
	if err != nil {
		t.Fatal(err)
	}
	return delivery
}

func readRunLaunch(t *testing.T, database *Store, launchID string) RunLaunch {
	t.Helper()
	launch, err := scanRunLaunch(database.db.QueryRow(runLaunchSelect+` WHERE id = ?`, launchID))
	if err != nil {
		t.Fatal(err)
	}
	return launch
}

func readInboxItemForDelivery(t *testing.T, database *Store, deliveryID string) InboxItem {
	t.Helper()
	var itemID string
	if err := database.db.QueryRow(`SELECT inbox_item_id FROM deliveries WHERE id = ?`, deliveryID).Scan(&itemID); err != nil {
		t.Fatal(err)
	}
	return readInboxItem(t, database, itemID)
}

func assertRunWrites(t *testing.T, database *Store, requestID string, wantRuns, wantLaunches int) {
	t.Helper()
	var requests, runs, launches, audits int
	if err := database.db.QueryRow(`SELECT count(*) FROM inbox_requests WHERE request_id = ?`, requestID).Scan(&requests); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRow(`SELECT count(*) FROM runs`).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRow(`SELECT count(*) FROM run_launches`).Scan(&launches); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRow(`SELECT count(*) FROM audit_events WHERE request_id = ?`, requestID).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if requests != 0 || runs != wantRuns || launches != wantLaunches || audits != 0 {
		t.Fatalf("run writes = requests:%d runs:%d launches:%d audits:%d, want 0/%d/%d/0", requests, runs, launches, audits, wantRuns, wantLaunches)
	}
}

func assertRunCompletionCounts(t *testing.T, database *Store, runID, requestID string, wantMessages, wantRequests, wantReceipts int) {
	t.Helper()
	var messages, requests, receipts int
	if err := database.db.QueryRow(`SELECT count(*) FROM messages WHERE request_id = ?`, requestID).Scan(&messages); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRow(`SELECT count(*) FROM inbox_requests WHERE request_id = ?`, requestID).Scan(&requests); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRow(`SELECT count(*) FROM run_completion_receipts WHERE run_id = ?`, runID).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if messages != wantMessages || requests != wantRequests || receipts != wantReceipts {
		t.Fatalf("completion counts = %d/%d/%d, want %d/%d/%d", messages, requests, receipts, wantMessages, wantRequests, wantReceipts)
	}
}

func assertRunAudit(t *testing.T, database *Store, requestID, action string) {
	t.Helper()
	var count int
	if err := database.db.QueryRow(`SELECT count(*) FROM audit_events WHERE request_id = ? AND action = ?`, requestID, action).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("audit events for request %s action %s = %d", requestID, action, count)
	}
}

func assertForeignKeysClean(t *testing.T, database *sql.DB) {
	t.Helper()
	rows, err := database.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("foreign key check returned a violation")
	}
}

func TestDeliverySchemaRejectsInvalidLifecycleFacts(t *testing.T) {
	fixture := openDeliveryFixture(t)
	first := fixture.createTrigger(t, "schema first", 1)
	second := fixture.createTrigger(t, "schema second", 2)
	if _, err := fixture.database.ObserveTarget(context.Background(), ObserveTargetParams{
		Authentication: AgentInboxAuthentication(fixture.authentication), Target: first.Item.Target, Limit: 200, Now: fixture.at(3),
	}); err != nil {
		t.Fatal(err)
	}
	run, err := fixture.database.AcceptDelivery(context.Background(), AcceptDeliveryParams{
		RequestID: uuid.NewString(), Authentication: fixture.authentication,
		DeliveryID: first.Delivery.ID, Now: fixture.at(4),
	})
	if err != nil {
		t.Fatal(err)
	}
	launch := fixture.claimRun(t, run, 5)
	if _, err := fixture.database.db.Exec(`UPDATE deliveries SET state = 'accepted' WHERE id = ?`, second.Delivery.ID); err == nil {
		t.Fatal("delivery lifecycle CHECK accepted an invalid transition fact")
	}
	if _, err := fixture.database.db.Exec(`
		INSERT INTO runs(id, delivery_id, agent_id, basis_target_sequence, state, accepted_at)
		VALUES(?, ?, ?, 1, 'accepted', ?)
	`, uuid.NewString(), second.Delivery.ID, fixture.agentID, unixNano(fixture.at(5))); err == nil {
		t.Fatal("one-active-run index accepted a second active run")
	}
	if _, err := fixture.database.db.Exec(`UPDATE runs SET state = 'completed' WHERE id = ?`, run.ID); err == nil {
		t.Fatal("run lifecycle CHECK accepted an incomplete completion fact")
	}
	if _, err := fixture.database.db.Exec(`
		UPDATE run_launches
		SET closed_at = ?, close_reason = 'replaced'
		WHERE id = ?
	`, unixNano(launch.ClaimedAt.Add(time.Second)), launch.ID); err == nil {
		t.Fatal("launch lifecycle CHECK accepted replacement before expiry")
	}
	if _, err := fixture.database.db.Exec(`
		INSERT INTO run_launches(
			id, run_id, agent_id, holder_computer_id, holder_placement_generation,
			fence, claimed_at, expires_at
		)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)
	`, uuid.NewString(), run.ID, fixture.agentID, launch.HolderComputerID,
		launch.HolderPlacementGeneration, launch.Fence, unixNano(fixture.at(6)), unixNano(fixture.at(66))); err == nil {
		t.Fatal("launch uniqueness accepted a duplicate Agent fence")
	}
	if _, err := fixture.database.db.Exec(`
		INSERT INTO run_completion_receipts(
			outbox_event_id, request_id, payload_fingerprint, run_id, launch_id,
			fence, result_kind, result_id, committed_at
		)
		VALUES(?, ?, ?, ?, ?, ?, 'message', ?, ?)
	`, uuid.NewString(), uuid.NewString(), make([]byte, 32), run.ID, launch.ID,
		launch.Fence, uuid.NewString(), unixNano(fixture.at(7))); err == nil {
		t.Fatal("completion receipt accepted a request outside the global registry")
	}
	requestID := uuid.NewString()
	if _, err := fixture.database.db.Exec(`
		INSERT INTO inbox_requests(request_id, actor_kind, actor_id, operation, payload_fingerprint, response_snapshot, committed_at)
		VALUES(?, 'agent', ?, 'run.complete', ?, '{}', ?)
	`, requestID, fixture.agentID, make([]byte, 32), unixNano(fixture.at(7))); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.db.Exec(`
		INSERT INTO run_completion_receipts(
			outbox_event_id, request_id, payload_fingerprint, run_id, launch_id,
			fence, result_kind, result_id, committed_at
		)
		VALUES(?, ?, ?, ?, ?, ?, 'message', ?, ?)
	`, uuid.NewString(), requestID, make([]byte, 32), run.ID, launch.ID,
		launch.Fence+1, uuid.NewString(), unixNano(fixture.at(7))); err == nil {
		t.Fatal("completion receipt accepted a fence that did not belong to its launch")
	}
	assertForeignKeysClean(t, fixture.database.db)
}

func TestDeliveryConcurrentAcceptHasOneWinner(t *testing.T) {
	fixture := openDeliveryFixture(t)
	first := fixture.createTrigger(t, "concurrent first", 1)
	second := fixture.createTrigger(t, "concurrent second", 2)
	if _, err := fixture.database.ObserveTarget(context.Background(), ObserveTargetParams{
		Authentication: AgentInboxAuthentication(fixture.authentication), Target: first.Item.Target, Limit: 200, Now: fixture.at(3),
	}); err != nil {
		t.Fatal(err)
	}
	deliveries := []Delivery{first.Delivery, second.Delivery}
	errorsByIndex := make([]error, len(deliveries))
	var wait sync.WaitGroup
	for index := range deliveries {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			_, errorsByIndex[index] = fixture.database.AcceptDelivery(context.Background(), AcceptDeliveryParams{
				RequestID: uuid.NewString(), Authentication: fixture.authentication,
				DeliveryID: deliveries[index].ID, Now: fixture.at(4),
			})
		}(index)
	}
	wait.Wait()
	var succeeded, active int
	for _, err := range errorsByIndex {
		if err == nil {
			succeeded++
		} else if errors.Is(err, ErrRunAlreadyActive) {
			active++
		} else {
			t.Fatalf("concurrent accept error = %v", err)
		}
	}
	if succeeded != 1 || active != 1 {
		t.Fatalf("concurrent accepts = success:%d active:%d", succeeded, active)
	}
}
