package delivery

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	deliveryv1 "github.com/abcdlsj/sumi/gen/go/sumi/delivery/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/delivery/v1/deliveryv1connect"
	"github.com/abcdlsj/sumi/internal/runtimeauth"
	"github.com/abcdlsj/sumi/internal/store"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	_ "modernc.org/sqlite"
)

func TestDeliveryServiceRuntimeAuthReplayFreshHeldAndQuiet(t *testing.T) {
	fixture := openServiceFixture(t)
	client := serviceClient(t, fixture, fixture.database)
	ctx := context.Background()
	if _, err := client.ListDeliveries(ctx, connect.NewRequest(&deliveryv1.ListDeliveriesRequest{})); connectCode(err) != connect.CodeUnauthenticated {
		t.Fatalf("unauthenticated list code = %v, error = %v", connectCode(err), err)
	}
	listed, err := client.ListDeliveries(ctx, runtimeRequest(fixture.token, &deliveryv1.ListDeliveriesRequest{Limit: 20}))
	if err != nil || len(listed.Msg.GetDeliveries()) != 1 || listed.Msg.GetActiveRun() != nil || listed.Msg.GetActiveLaunch() != nil {
		t.Fatalf("initial deliveries = %+v, %v", listed, err)
	}
	delivery := listed.Msg.GetDeliveries()[0]
	if delivery.GetTriggerMessageId() != fixture.trigger.ID || delivery.GetState() != deliveryv1.DeliveryState_DELIVERY_STATE_AVAILABLE ||
		delivery.GetTarget().GetSpaceId() != fixture.group.ID {
		t.Fatalf("delivery mapping = %+v", delivery)
	}
	acceptRequest := &deliveryv1.AcceptDeliveryRequest{RequestId: uuid.NewString(), DeliveryId: delivery.GetId()}
	if _, err := client.AcceptDelivery(ctx, runtimeRequest(fixture.token, acceptRequest)); connectCode(err) != connect.CodeFailedPrecondition {
		t.Fatalf("accept without cursor code = %v, error = %v", connectCode(err), err)
	}
	fixture.observe(t, fixture.trigger.Target)
	accepted, err := client.AcceptDelivery(ctx, runtimeRequest(fixture.token, acceptRequest))
	if err != nil || accepted.Msg.GetRun().GetState() != deliveryv1.RunState_RUN_STATE_ACCEPTED || accepted.Msg.GetRun().GetResultRef() != nil {
		t.Fatalf("accepted run = %+v, %v", accepted, err)
	}
	fixture.advance()
	replayedAccept, err := client.AcceptDelivery(ctx, runtimeRequest(fixture.token, acceptRequest))
	if err != nil || !proto.Equal(accepted.Msg, replayedAccept.Msg) {
		t.Fatalf("accept replay = %+v, %v", replayedAccept, err)
	}
	activeAccepted, err := client.ListDeliveries(ctx, runtimeRequest(fixture.token, &deliveryv1.ListDeliveriesRequest{Limit: 20}))
	if err != nil || len(activeAccepted.Msg.GetDeliveries()) != 0 || activeAccepted.Msg.GetActiveRun().GetId() != accepted.Msg.GetRun().GetId() || activeAccepted.Msg.GetActiveLaunch() != nil {
		t.Fatalf("accepted discovery = %+v, %v", activeAccepted, err)
	}
	gotRun, err := client.GetRun(ctx, runtimeRequest(fixture.token, &deliveryv1.GetRunRequest{RunId: accepted.Msg.GetRun().GetId()}))
	if err != nil || !proto.Equal(gotRun.Msg.GetRun(), accepted.Msg.GetRun()) {
		t.Fatalf("get accepted run = %+v, %v", gotRun, err)
	}
	claimRequest := &deliveryv1.ClaimRunRequest{RequestId: uuid.NewString(), RunId: accepted.Msg.GetRun().GetId()}
	claimed, err := client.ClaimRun(ctx, runtimeRequest(fixture.token, claimRequest))
	if err != nil || claimed.Msg.GetLaunch().GetFence() == 0 || claimed.Msg.GetLaunch().GetHolderComputerId() != fixture.computer.ID {
		t.Fatalf("claimed launch = %+v, %v", claimed, err)
	}
	fixture.advance()
	replayedClaim, err := client.ClaimRun(ctx, runtimeRequest(fixture.token, claimRequest))
	if err != nil || !proto.Equal(claimed.Msg, replayedClaim.Msg) {
		t.Fatalf("claim replay = %+v, %v", replayedClaim, err)
	}
	activeRunning, err := client.ListDeliveries(ctx, runtimeRequest(fixture.token, &deliveryv1.ListDeliveriesRequest{Limit: 20}))
	if err != nil || activeRunning.Msg.GetActiveRun().GetState() != deliveryv1.RunState_RUN_STATE_RUNNING ||
		!proto.Equal(activeRunning.Msg.GetActiveLaunch(), claimed.Msg.GetLaunch()) {
		t.Fatalf("running discovery = %+v, %v", activeRunning, err)
	}
	renewRequest := &deliveryv1.RenewRunRequest{
		RequestId: uuid.NewString(), RunId: accepted.Msg.GetRun().GetId(),
		LaunchId: claimed.Msg.GetLaunch().GetId(), Fence: claimed.Msg.GetLaunch().GetFence(),
	}
	renewed, err := client.RenewRun(ctx, runtimeRequest(fixture.token, renewRequest))
	if err != nil || !renewed.Msg.GetLaunch().GetExpiresAt().AsTime().After(claimed.Msg.GetLaunch().GetExpiresAt().AsTime()) {
		t.Fatalf("renewed launch = %+v, %v", renewed, err)
	}
	fixture.advance()
	replayedRenew, err := client.RenewRun(ctx, runtimeRequest(fixture.token, renewRequest))
	if err != nil || !proto.Equal(renewed.Msg, replayedRenew.Msg) {
		t.Fatalf("renew replay = %+v, %v", replayedRenew, err)
	}
	freshBody := "delivery-service-fresh-body-7f91"
	freshRequest := &deliveryv1.CompleteRunRequest{
		RequestId: uuid.NewString(), OutboxEventId: uuid.NewString(), RunId: accepted.Msg.GetRun().GetId(),
		LaunchId: claimed.Msg.GetLaunch().GetId(), Fence: claimed.Msg.GetLaunch().GetFence(),
		Outcome: deliveryv1.RunOutcome_RUN_OUTCOME_SUCCEEDED, Body: freshBody,
		MentionedAgentIds: []string{fixture.peer.ID},
	}
	invalidOutcome := proto.Clone(freshRequest).(*deliveryv1.CompleteRunRequest)
	invalidOutcome.Outcome = deliveryv1.RunOutcome_RUN_OUTCOME_UNSPECIFIED
	if _, err := client.CompleteRun(ctx, runtimeRequest(fixture.token, invalidOutcome)); connectCode(err) != connect.CodeInvalidArgument {
		t.Fatalf("invalid outcome code = %v, error = %v", connectCode(err), err)
	}
	invalidBody := proto.Clone(freshRequest).(*deliveryv1.CompleteRunRequest)
	invalidBody.Body = ""
	if _, err := client.CompleteRun(ctx, runtimeRequest(fixture.token, invalidBody)); connectCode(err) != connect.CodeInvalidArgument {
		t.Fatalf("invalid body code = %v, error = %v", connectCode(err), err)
	}
	invalidMentions := proto.Clone(freshRequest).(*deliveryv1.CompleteRunRequest)
	invalidMentions.MentionedAgentIds = []string{fixture.peer.ID, fixture.peer.ID}
	if _, err := client.CompleteRun(ctx, runtimeRequest(fixture.token, invalidMentions)); connectCode(err) != connect.CodeInvalidArgument {
		t.Fatalf("invalid mentions code = %v, error = %v", connectCode(err), err)
	}
	fresh, err := client.CompleteRun(ctx, runtimeRequest(fixture.token, freshRequest))
	if err != nil || fresh.Msg.GetMessage() == nil || fresh.Msg.GetHeldDraft() != nil ||
		fresh.Msg.GetRun().GetState() != deliveryv1.RunState_RUN_STATE_COMPLETED ||
		fresh.Msg.GetRun().GetResultMessageId() != fresh.Msg.GetMessage().GetId() ||
		fresh.Msg.GetMessage().GetBody() != freshBody || !reflect.DeepEqual(fresh.Msg.GetMessage().GetMentionedAgentIds(), []string{fixture.peer.ID}) {
		t.Fatalf("fresh completion = %+v, %v", fresh, err)
	}
	fixture.advance()
	replayedFresh, err := client.CompleteRun(ctx, runtimeRequest(fixture.token, freshRequest))
	if err != nil || !proto.Equal(fresh.Msg, replayedFresh.Msg) {
		t.Fatalf("fresh replay = %+v, %v", replayedFresh, err)
	}
	conflicts := []*deliveryv1.CompleteRunRequest{
		{RequestId: uuid.NewString(), OutboxEventId: freshRequest.OutboxEventId},
		{RequestId: freshRequest.RequestId, OutboxEventId: uuid.NewString()},
		{RequestId: uuid.NewString(), OutboxEventId: uuid.NewString()},
	}
	for _, conflict := range conflicts {
		conflict.RunId = freshRequest.RunId
		conflict.LaunchId = freshRequest.LaunchId
		conflict.Fence = freshRequest.Fence
		conflict.Outcome = freshRequest.Outcome
		conflict.Body = freshBody + "-conflict"
		if _, err := client.CompleteRun(ctx, runtimeRequest(fixture.token, conflict)); connectCode(err) != connect.CodeAlreadyExists {
			t.Fatalf("completion conflict code = %v, error = %v", connectCode(err), err)
		}
	}
	fixture.advance()
	secondTrigger := fixture.sendTrigger(t, "held trigger")
	secondList, err := client.ListDeliveries(ctx, runtimeRequest(fixture.token, &deliveryv1.ListDeliveriesRequest{Limit: 20}))
	if err != nil || len(secondList.Msg.GetDeliveries()) != 1 || secondList.Msg.GetDeliveries()[0].GetTriggerMessageId() != secondTrigger.ID {
		t.Fatalf("second delivery = %+v, %v", secondList, err)
	}
	fixture.observe(t, secondTrigger.Target)
	secondAccept, err := client.AcceptDelivery(ctx, runtimeRequest(fixture.token, &deliveryv1.AcceptDeliveryRequest{
		RequestId: uuid.NewString(), DeliveryId: secondList.Msg.GetDeliveries()[0].GetId(),
	}))
	if err != nil {
		t.Fatal(err)
	}
	secondClaim, err := client.ClaimRun(ctx, runtimeRequest(fixture.token, &deliveryv1.ClaimRunRequest{
		RequestId: uuid.NewString(), RunId: secondAccept.Msg.GetRun().GetId(),
	}))
	if err != nil {
		t.Fatal(err)
	}
	fixture.advance()
	if _, err := fixture.database.SendMessage(ctx, store.SendMessageParams{
		RequestID: uuid.NewString(), Actor: fixture.owner, Target: secondTrigger.Target,
		Body: "advance held target", Now: fixture.current,
	}); err != nil {
		t.Fatal(err)
	}
	fixture.advance()
	heldBody := "delivery-service-held-body-4ac2"
	heldRequest := &deliveryv1.CompleteRunRequest{
		RequestId: uuid.NewString(), OutboxEventId: uuid.NewString(), RunId: secondAccept.Msg.GetRun().GetId(),
		LaunchId: secondClaim.Msg.GetLaunch().GetId(), Fence: secondClaim.Msg.GetLaunch().GetFence(),
		Outcome: deliveryv1.RunOutcome_RUN_OUTCOME_FAILED, Body: heldBody,
	}
	staleRequest := proto.Clone(heldRequest).(*deliveryv1.CompleteRunRequest)
	staleRequest.RequestId = uuid.NewString()
	staleRequest.OutboxEventId = uuid.NewString()
	staleRequest.Fence++
	if _, err := client.CompleteRun(ctx, runtimeRequest(fixture.token, staleRequest)); connectCode(err) != connect.CodeFailedPrecondition {
		t.Fatalf("stale completion code = %v, error = %v", connectCode(err), err)
	}
	held, err := client.CompleteRun(ctx, runtimeRequest(fixture.token, heldRequest))
	if err != nil || held.Msg.GetHeldDraft() == nil || held.Msg.GetMessage() != nil ||
		held.Msg.GetRun().GetResultHeldDraftId() != held.Msg.GetHeldDraft().GetId() || held.Msg.GetHeldDraft().GetBody() != heldBody {
		t.Fatalf("held completion = %+v, %v", held, err)
	}
	fixture.advance()
	replayedHeld, err := client.CompleteRun(ctx, runtimeRequest(fixture.token, heldRequest))
	if err != nil || !proto.Equal(held.Msg, replayedHeld.Msg) {
		t.Fatalf("held replay = %+v, %v", replayedHeld, err)
	}
	finalList, err := client.ListDeliveries(ctx, runtimeRequest(fixture.token, &deliveryv1.ListDeliveriesRequest{Limit: 20}))
	if err != nil || len(finalList.Msg.GetDeliveries()) != 0 || finalList.Msg.GetActiveRun() != nil || finalList.Msg.GetActiveLaunch() != nil {
		t.Fatalf("final deliveries = %+v, %v", finalList, err)
	}
	assertRegistryQuiet(t, fixture.path, freshBody, heldBody, fixture.peer.ID, fixture.token)
}

func TestDeliveryServiceRuntimeHolderReplay(t *testing.T) {
	fixture := openServiceFixture(t)
	client := serviceClient(t, fixture, fixture.database)
	ctx := context.Background()
	listed, err := client.ListDeliveries(ctx, runtimeRequest(fixture.token, &deliveryv1.ListDeliveriesRequest{Limit: 20}))
	if err != nil || len(listed.Msg.GetDeliveries()) != 1 {
		t.Fatalf("initial deliveries = %+v, %v", listed, err)
	}
	fixture.observe(t, fixture.trigger.Target)
	accepted, err := client.AcceptDelivery(ctx, runtimeRequest(fixture.token, &deliveryv1.AcceptDeliveryRequest{
		RequestId: uuid.NewString(), DeliveryId: listed.Msg.GetDeliveries()[0].GetId(),
	}))
	if err != nil {
		t.Fatal(err)
	}
	claimRequest := &deliveryv1.ClaimRunRequest{RequestId: uuid.NewString(), RunId: accepted.Msg.GetRun().GetId()}
	claimed, err := client.ClaimRun(ctx, runtimeRequest(fixture.token, claimRequest))
	if err != nil {
		t.Fatal(err)
	}
	rotatedToken := base64.RawURLEncoding.EncodeToString([]byte("00112233445566778899aabbccddeeff"))
	if _, err := fixture.database.CreateAgentRuntimeSession(ctx, store.CreateAgentRuntimeSessionParams{
		ComputerID: fixture.computer.ID, RegistrationKey: fixture.registrationKey,
		AgentID: fixture.agent.ID, PlacementGeneration: fixture.placementGeneration,
		Token: rotatedToken, Now: fixture.current, ExpiresAt: fixture.current.Add(10 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	fixture.advance()
	replayed, err := client.ClaimRun(ctx, runtimeRequest(rotatedToken, claimRequest))
	if err != nil || !proto.Equal(replayed.Msg, claimed.Msg) {
		t.Fatalf("claim replay after token rotation = %+v, %v", replayed, err)
	}
	registrationKey := "delivery-migrated-computer-registration-key"
	computer, err := fixture.database.RegisterComputer(ctx, store.RegisterComputerParams{
		RegistrationKey: registrationKey, Name: "delivery-migrated", OS: "linux", Arch: "arm64", Now: fixture.current,
	})
	if err != nil {
		t.Fatal(err)
	}
	placement, err := fixture.database.SetAgentPlacement(ctx, store.SetAgentPlacementParams{
		RequestID: uuid.NewString(), Actor: fixture.owner, AgentID: fixture.agent.ID,
		ComputerID: computer.ID, Now: fixture.current.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.AcknowledgeAgentPlacement(ctx, store.AcknowledgePlacementParams{
		ComputerID: computer.ID, RegistrationKey: registrationKey, AgentID: fixture.agent.ID,
		Generation: placement.Generation, State: "active", Now: fixture.current.Add(2 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	migratedToken := base64.RawURLEncoding.EncodeToString([]byte("ffeeddccbbaa99887766554433221100"))
	if _, err := fixture.database.CreateAgentRuntimeSession(ctx, store.CreateAgentRuntimeSessionParams{
		ComputerID: computer.ID, RegistrationKey: registrationKey, AgentID: fixture.agent.ID,
		PlacementGeneration: placement.Generation, Token: migratedToken,
		Now: fixture.current.Add(3 * time.Second), ExpiresAt: fixture.current.Add(10*time.Minute + 3*time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	fixture.current = fixture.current.Add(4 * time.Second)
	if _, err := client.ClaimRun(ctx, runtimeRequest(migratedToken, claimRequest)); connectCode(err) != connect.CodeFailedPrecondition {
		t.Fatalf("claim replay after placement migration code = %v, error = %v", connectCode(err), err)
	}
}

func TestDeliveryServiceConnectErrorCodes(t *testing.T) {
	fixture := openServiceFixture(t)
	stub := &stubStore{}
	client := serviceClient(t, fixture, stub)
	tests := map[connect.Code]error{
		connect.CodeUnauthenticated:    store.ErrAgentRuntimeUnauthenticated,
		connect.CodePermissionDenied:   store.ErrInboxAccessLost,
		connect.CodeNotFound:           store.ErrRunNotFound,
		connect.CodeAlreadyExists:      store.ErrRunCompletionConflict,
		connect.CodeFailedPrecondition: store.ErrRunLaunchStale,
		connect.CodeResourceExhausted:  store.ErrRunAlreadyActive,
		connect.CodeInvalidArgument:    store.ErrRunInvalidOutcome,
		connect.CodeInternal:           store.ErrRunIntegrity,
	}
	for want, input := range tests {
		stub.err = input
		if _, err := client.ListDeliveries(context.Background(), runtimeRequest(fixture.token, &deliveryv1.ListDeliveriesRequest{Limit: 20})); connectCode(err) != want {
			t.Fatalf("error %v code = %v, want %v", input, connectCode(err), want)
		}
	}
	stub.err = nil
	if _, err := client.ListDeliveries(context.Background(), runtimeRequest(fixture.token, &deliveryv1.ListDeliveriesRequest{Limit: 201})); connectCode(err) != connect.CodeInvalidArgument {
		t.Fatalf("invalid limit code = %v, error = %v", connectCode(err), err)
	}
	if _, err := client.RenewRun(context.Background(), runtimeRequest(fixture.token, &deliveryv1.RenewRunRequest{
		RequestId: uuid.NewString(), RunId: uuid.NewString(), LaunchId: uuid.NewString(), Fence: math.MaxUint64,
	})); connectCode(err) != connect.CodeInvalidArgument {
		t.Fatalf("invalid fence code = %v, error = %v", connectCode(err), err)
	}
}

func TestDeliveryServiceRejectsImpossibleStoreResults(t *testing.T) {
	fixture := openServiceFixture(t)
	stub := &stubStore{}
	client := serviceClient(t, fixture, stub)
	now := fixture.current
	runID := uuid.NewString()
	agentID := fixture.agent.ID
	deliveryID := uuid.NewString()
	accepted := store.Run{
		ID: runID, DeliveryID: deliveryID, AgentID: agentID,
		BasisTargetSequence: 1, State: store.RunStateAccepted, AcceptedAt: now,
	}
	startedAt := now.Add(time.Second)
	running := accepted
	running.State = store.RunStateRunning
	running.StartedAt = &startedAt
	launch := store.RunLaunch{
		ID: uuid.NewString(), RunID: runID, AgentID: agentID,
		HolderComputerID: fixture.computer.ID, HolderPlacementGeneration: 1, Fence: 1,
		ClaimedAt: now, ExpiresAt: now.Add(time.Minute),
	}
	listCases := []store.ListDeliveriesResult{
		{ActiveRun: &accepted, ActiveLaunch: &launch},
		{ActiveRun: &running},
		{ActiveLaunch: &launch},
		{Deliveries: []store.Delivery{{State: "impossible"}}},
	}
	for _, result := range listCases {
		stub.listResult = result
		if _, err := client.ListDeliveries(context.Background(), runtimeRequest(fixture.token, &deliveryv1.ListDeliveriesRequest{Limit: 20})); connectCode(err) != connect.CodeInternal {
			t.Fatalf("impossible list result %+v code = %v, error = %v", result, connectCode(err), err)
		}
	}
	stub.run = accepted
	stub.run.State = "impossible"
	if _, err := client.GetRun(context.Background(), runtimeRequest(fixture.token, &deliveryv1.GetRunRequest{RunId: runID})); connectCode(err) != connect.CodeInternal {
		t.Fatalf("impossible run code = %v, error = %v", connectCode(err), err)
	}
	closedAt := now.Add(time.Second)
	launchCases := []store.RunLaunch{
		func() store.RunLaunch {
			value := launch
			value.ClosedAt = &closedAt
			return value
		}(),
		func() store.RunLaunch {
			value := launch
			value.ClosedAt = &closedAt
			value.CloseReason = store.RunLaunchCloseReplaced
			return value
		}(),
		func() store.RunLaunch {
			value := launch
			value.CloseReason = store.RunLaunchCloseCompleted
			return value
		}(),
	}
	for _, value := range launchCases {
		stub.launch = value
		if _, err := client.ClaimRun(context.Background(), runtimeRequest(fixture.token, &deliveryv1.ClaimRunRequest{
			RequestId: uuid.NewString(), RunId: runID,
		})); connectCode(err) != connect.CodeInternal {
			t.Fatalf("impossible launch %+v code = %v, error = %v", value, connectCode(err), err)
		}
	}
	completedAt := now.Add(2 * time.Second)
	completed := running
	completed.State = store.RunStateCompleted
	completed.Outcome = store.RunOutcomeSucceeded
	completed.ResultKind = store.InboxResultMessage
	completed.ResultID = uuid.NewString()
	completed.CompletedAt = &completedAt
	stub.completeResult = store.CompleteRunResult{
		Run: completed, Kind: store.InboxResultMessage,
		HeldDraft: &store.HeldDraft{ID: completed.ResultID}, CommittedAt: completedAt,
	}
	if _, err := client.CompleteRun(context.Background(), runtimeRequest(fixture.token, &deliveryv1.CompleteRunRequest{
		RequestId: uuid.NewString(), OutboxEventId: uuid.NewString(), RunId: runID,
		LaunchId: launch.ID, Fence: 1, Outcome: deliveryv1.RunOutcome_RUN_OUTCOME_SUCCEEDED, Body: "result",
	})); connectCode(err) != connect.CodeInternal {
		t.Fatalf("impossible completion code = %v, error = %v", connectCode(err), err)
	}
	completeRequest := &deliveryv1.CompleteRunRequest{
		RequestId: uuid.NewString(), OutboxEventId: uuid.NewString(), RunId: runID,
		LaunchId: launch.ID, Fence: 1, Outcome: deliveryv1.RunOutcome_RUN_OUTCOME_SUCCEEDED, Body: "result",
	}
	draftID := uuid.NewString()
	completed.ResultKind = store.InboxResultHeldDraft
	completed.ResultID = draftID
	draftBase := store.HeldDraft{
		ID: draftID, InboxItemID: uuid.NewString(), AgentID: agentID, SpaceID: fixture.group.ID,
		Target:              store.MessageTarget{Kind: store.MessageTargetSpace, ID: fixture.group.ID},
		BasisTargetSequence: 1, Body: "result", CreatedAt: now, UpdatedAt: now,
	}
	draftCases := []store.HeldDraft{
		func() store.HeldDraft {
			value := draftBase
			value.State = store.HeldDraftStateHeld
			value.ResolutionAction = store.DraftResolutionRetry
			value.ResultKind = store.InboxResultMessage
			value.ResultID = uuid.NewString()
			return value
		}(),
		func() store.HeldDraft {
			value := draftBase
			value.State = store.HeldDraftStateSent
			value.ResolutionAction = store.DraftResolutionCancel
			return value
		}(),
		func() store.HeldDraft {
			value := draftBase
			value.State = store.HeldDraftStateRetargeted
			value.ResolutionAction = store.DraftResolutionRetarget
			return value
		}(),
	}
	for _, draft := range draftCases {
		stub.completeResult = store.CompleteRunResult{
			Run: completed, Kind: store.InboxResultHeldDraft, HeldDraft: &draft, CommittedAt: completedAt,
		}
		if _, err := client.CompleteRun(context.Background(), runtimeRequest(fixture.token, completeRequest)); connectCode(err) != connect.CodeInternal {
			t.Fatalf("impossible draft %+v code = %v, error = %v", draft, connectCode(err), err)
		}
	}
	messageID := uuid.NewString()
	completed.ResultKind = store.InboxResultMessage
	completed.ResultID = messageID
	messageBase := store.Message{
		ID: messageID, RequestID: uuid.NewString(), SpaceID: fixture.group.ID,
		Author: store.Principal{Kind: "agent", ID: agentID}, Body: "result", CreatedAt: now,
	}
	messageCases := []store.Message{
		func() store.Message {
			value := messageBase
			value.Target = store.MessageTarget{Kind: "impossible", ID: fixture.group.ID}
			return value
		}(),
		func() store.Message {
			value := messageBase
			value.Target = store.MessageTarget{Kind: store.MessageTargetSpace, ID: uuid.NewString()}
			return value
		}(),
		func() store.Message {
			value := messageBase
			value.Target = store.MessageTarget{Kind: store.MessageTargetThread, ID: "not-a-root"}
			return value
		}(),
	}
	for _, message := range messageCases {
		stub.completeResult = store.CompleteRunResult{
			Run: completed, Kind: store.InboxResultMessage, Message: &message, CommittedAt: completedAt,
		}
		if _, err := client.CompleteRun(context.Background(), runtimeRequest(fixture.token, completeRequest)); connectCode(err) != connect.CodeInternal {
			t.Fatalf("impossible message %+v code = %v, error = %v", message, connectCode(err), err)
		}
	}
}

func TestDeliveryProceduresCoverGeneratedService(t *testing.T) {
	want := []string{
		deliveryv1connect.DeliveryServiceListDeliveriesProcedure,
		deliveryv1connect.DeliveryServiceAcceptDeliveryProcedure,
		deliveryv1connect.DeliveryServiceGetRunProcedure,
		deliveryv1connect.DeliveryServiceClaimRunProcedure,
		deliveryv1connect.DeliveryServiceRenewRunProcedure,
		deliveryv1connect.DeliveryServiceCompleteRunProcedure,
	}
	if !reflect.DeepEqual(Procedures(), want) {
		t.Fatalf("procedures = %v, want %v", Procedures(), want)
	}
}

type serviceFixture struct {
	database            *store.Store
	path                string
	owner               store.Principal
	agent               store.Agent
	peer                store.Agent
	computer            store.Computer
	registrationKey     string
	placementGeneration uint64
	group               store.Space
	trigger             store.Message
	authentication      store.AgentRuntimeAuthentication
	token               string
	current             time.Time
}

func openServiceFixture(t *testing.T) *serviceFixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "server.db")
	database, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Error(err)
		}
	})
	ctx := context.Background()
	base := time.Now().UTC().Add(-time.Second)
	bootstrap, err := database.EnsureAuthority(ctx, "delivery-service-owner-credential-abcdefghijklmnopqrstuvwxyz", base)
	if err != nil {
		t.Fatal(err)
	}
	owner := store.Principal{Kind: "human", ID: bootstrap.Human.ID, OrganizationID: bootstrap.Organization.ID}
	agent, err := database.CreateAgent(ctx, store.CreateAgentParams{
		RequestID: uuid.NewString(), Actor: owner, Name: "delivery-service", Driver: "native", Now: base.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	peer, err := database.CreateAgent(ctx, store.CreateAgentParams{
		RequestID: uuid.NewString(), Actor: owner, Name: "delivery-peer", Driver: "native", Now: base.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	registrationKey := "delivery-service-computer-registration-key"
	computer, err := database.RegisterComputer(ctx, store.RegisterComputerParams{
		RegistrationKey: registrationKey, Name: "delivery-host", OS: "linux", Arch: "arm64", Now: base.Add(3 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	placement, err := database.SetAgentPlacement(ctx, store.SetAgentPlacementParams{
		RequestID: uuid.NewString(), Actor: owner, AgentID: agent.ID, ComputerID: computer.ID, Now: base.Add(4 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.AcknowledgeAgentPlacement(ctx, store.AcknowledgePlacementParams{
		ComputerID: computer.ID, RegistrationKey: registrationKey, AgentID: agent.ID,
		Generation: placement.Generation, State: "active", Now: base.Add(5 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	token := base64.RawURLEncoding.EncodeToString([]byte("fedcba9876543210fedcba9876543210"))
	if _, err := database.CreateAgentRuntimeSession(ctx, store.CreateAgentRuntimeSessionParams{
		ComputerID: computer.ID, RegistrationKey: registrationKey, AgentID: agent.ID,
		PlacementGeneration: placement.Generation, Token: token,
		Now: base.Add(6 * time.Second), ExpiresAt: base.Add(10*time.Minute + 6*time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	group, err := database.CreateGroup(ctx, store.CreateGroupParams{
		RequestID: uuid.NewString(), Actor: owner, Name: "Delivery Service", Now: base.Add(7 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	for index, member := range []store.Agent{agent, peer} {
		if _, err := database.AddMember(ctx, store.ChangeMemberParams{
			RequestID: uuid.NewString(), Actor: owner, SpaceID: group.ID,
			Member: store.Principal{Kind: "agent", ID: member.ID}, Now: base.Add(time.Duration(8+index) * time.Second),
		}); err != nil {
			t.Fatal(err)
		}
	}
	grants := []struct {
		capability string
		scope      store.Scope
	}{
		{store.CapabilitySpaceRead, store.Scope{Kind: "space", ID: group.ID}},
		{store.CapabilityMessageSend, store.Scope{Kind: "space", ID: group.ID}},
		{store.CapabilityRunExecute, store.Scope{Kind: "agent", ID: agent.ID}},
	}
	for index, grant := range grants {
		if _, err := database.IssueGrant(ctx, store.IssueGrantParams{
			RequestID: uuid.NewString(), Actor: owner, Subject: store.Principal{Kind: "agent", ID: agent.ID},
			Capability: grant.capability, Scope: grant.scope, ParentGrantID: bootstrap.RootGrant.ID,
			Now: base.Add(time.Duration(10+index) * time.Second),
		}); err != nil {
			t.Fatal(err)
		}
	}
	current := base.Add(13 * time.Second)
	trigger, err := database.SendMessage(ctx, store.SendMessageParams{
		RequestID: uuid.NewString(), Actor: owner, Target: store.MessageTarget{Kind: store.MessageTargetSpace, ID: group.ID},
		Body: "delivery trigger", MentionedAgentIDs: []string{agent.ID}, Now: current,
	})
	if err != nil {
		t.Fatal(err)
	}
	authentication, err := database.AuthenticateAgentRuntimeSession(ctx, token, current)
	if err != nil {
		t.Fatal(err)
	}
	return &serviceFixture{
		database: database, path: path, owner: owner, agent: agent, peer: peer, computer: computer,
		registrationKey: registrationKey, placementGeneration: placement.Generation,
		group: group, trigger: trigger, authentication: authentication, token: token,
		current: current.Add(time.Second),
	}
}

func (f *serviceFixture) advance() {
	f.current = f.current.Add(time.Second)
}

func (f *serviceFixture) observe(t *testing.T, target store.MessageTarget) {
	t.Helper()
	if _, err := f.database.ObserveTarget(context.Background(), store.ObserveTargetParams{
		Authentication: f.authentication, Target: target, Limit: 200, Now: f.current,
	}); err != nil {
		t.Fatal(err)
	}
	f.advance()
}

func (f *serviceFixture) sendTrigger(t *testing.T, body string) store.Message {
	t.Helper()
	message, err := f.database.SendMessage(context.Background(), store.SendMessageParams{
		RequestID: uuid.NewString(), Actor: f.owner,
		Target: store.MessageTarget{Kind: store.MessageTargetSpace, ID: f.group.ID}, Body: body,
		MentionedAgentIDs: []string{f.agent.ID}, Now: f.current,
	})
	if err != nil {
		t.Fatal(err)
	}
	f.advance()
	return message
}

func serviceClient(t *testing.T, fixture *serviceFixture, database deliveryStore) deliveryv1connect.DeliveryServiceClient {
	t.Helper()
	service := New(database)
	service.now = func() time.Time { return fixture.current }
	path, handler := deliveryv1connect.NewDeliveryServiceHandler(service, connect.WithInterceptors(
		runtimeauth.NewProcedureInterceptor(fixture.database, Procedures()...),
	))
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return deliveryv1connect.NewDeliveryServiceClient(server.Client(), server.URL)
}

func runtimeRequest[T any](token string, message *T) *connect.Request[T] {
	request := connect.NewRequest(message)
	request.Header().Set("Authorization", "Bearer "+token)
	return request
}

func connectCode(err error) connect.Code {
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		return connect.CodeUnknown
	}
	return connectErr.Code()
}

func assertRegistryQuiet(t *testing.T, path string, forbidden ...string) {
	t.Helper()
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	rows, err := database.Query(`SELECT CAST(response_snapshot AS TEXT) FROM agent_requests`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var snapshot string
		if err := rows.Scan(&snapshot); err != nil {
			t.Fatal(err)
		}
		for _, value := range forbidden {
			if strings.Contains(snapshot, value) {
				t.Fatalf("agent request snapshot leaked %q: %s", value, snapshot)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

type stubStore struct {
	err            error
	listResult     store.ListDeliveriesResult
	run            store.Run
	launch         store.RunLaunch
	completeResult store.CompleteRunResult
}

func (s *stubStore) ListDeliveries(context.Context, store.ListDeliveriesParams) (store.ListDeliveriesResult, error) {
	return s.listResult, s.err
}

func (s *stubStore) AcceptDelivery(context.Context, store.AcceptDeliveryParams) (store.Run, error) {
	return s.run, s.err
}

func (s *stubStore) GetRun(context.Context, store.AgentRuntimeAuthentication, string, time.Time) (store.Run, error) {
	return s.run, s.err
}

func (s *stubStore) ClaimRun(context.Context, store.ClaimRunParams) (store.RunLaunch, error) {
	return s.launch, s.err
}

func (s *stubStore) RenewRun(context.Context, store.RenewRunParams) (store.RunLaunch, error) {
	return s.launch, s.err
}

func (s *stubStore) CompleteRun(context.Context, store.CompleteRunParams) (store.CompleteRunResult, error) {
	return s.completeResult, s.err
}
