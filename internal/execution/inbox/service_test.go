package inbox

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	inboxv1 "github.com/abcdlsj/sumi/gen/go/sumi/inbox/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/inbox/v1/inboxv1connect"
	spacev1 "github.com/abcdlsj/sumi/gen/go/sumi/space/v1"
	agentapp "github.com/abcdlsj/sumi/internal/agent/application"
	computerapp "github.com/abcdlsj/sumi/internal/computer/application"
	computerdomain "github.com/abcdlsj/sumi/internal/computer/domain"
	"github.com/abcdlsj/sumi/internal/store"
	"github.com/abcdlsj/sumi/internal/transport/messagecodec"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
)

func TestInboxServiceRuntimeAuthReplayErrorsAndHeldDraftMapping(t *testing.T) {
	f := openServiceFixture(t)
	service := New(f.database, "")
	service.now = func() time.Time { return f.current }
	path, handler := inboxv1connect.NewInboxServiceHandler(service)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)
	defer server.Close()
	client := inboxv1connect.NewInboxServiceClient(server.Client(), server.URL)

	if _, err := client.GetInboxNotice(context.Background(), connect.NewRequest(&inboxv1.GetInboxNoticeRequest{})); connectCode(err) != connect.CodeUnauthenticated {
		t.Fatalf("unauthenticated notice code = %v, error = %v", connectCode(err), err)
	}
	notice, err := client.GetInboxNotice(context.Background(), runtimeRequest(f.token, &inboxv1.GetInboxNoticeRequest{}))
	if err != nil || !notice.Msg.GetHasUnread() {
		t.Fatalf("notice = %+v, %v", notice, err)
	}
	listed, err := client.ListInboxItems(context.Background(), runtimeRequest(f.token, &inboxv1.ListInboxItemsRequest{Limit: 1}))
	if err != nil || len(listed.Msg.GetItems()) != 1 {
		t.Fatalf("items = %+v, %v", listed, err)
	}
	item := listed.Msg.GetItems()[0]
	if item.GetRecipient().GetKind() != spacev1.PrincipalKind_PRINCIPAL_KIND_AGENT || item.GetRecipient().GetId() != f.agent.ID ||
		item.GetReason() != inboxv1.InboxReason_INBOX_REASON_MENTION || item.GetSpaceId() != f.group.ID {
		t.Fatalf("item mapping = %+v", item)
	}
	muteRequest := &inboxv1.SetSpaceMuteRequest{RequestId: uuid.NewString(), SpaceId: f.group.ID, Muted: true}
	muted, err := client.SetSpaceMute(context.Background(), runtimeRequest(f.token, muteRequest))
	if err != nil || !muted.Msg.GetMuted() {
		t.Fatalf("mute = %+v, %v", muted, err)
	}
	f.current = f.current.Add(time.Second)
	replayedMute, err := client.SetSpaceMute(context.Background(), runtimeRequest(f.token, muteRequest))
	if err != nil || !proto.Equal(muted.Msg, replayedMute.Msg) {
		t.Fatalf("mute replay = %+v, %v", replayedMute, err)
	}
	followRequest := &inboxv1.SetThreadFollowRequest{
		RequestId: uuid.NewString(), ThreadRootMessageId: f.trigger.ID, Followed: true,
	}
	followed, err := client.SetThreadFollow(context.Background(), runtimeRequest(f.token, followRequest))
	if err != nil || !followed.Msg.GetFollowed() {
		t.Fatalf("follow = %+v, %v", followed, err)
	}
	f.current = f.current.Add(time.Second)
	replayedFollow, err := client.SetThreadFollow(context.Background(), runtimeRequest(f.token, followRequest))
	if err != nil || !proto.Equal(followed.Msg, replayedFollow.Msg) {
		t.Fatalf("follow replay = %+v, %v", replayedFollow, err)
	}
	claimRequestID := uuid.NewString()
	claimRequest := &inboxv1.ClaimInboxItemRequest{RequestId: claimRequestID, InboxItemId: item.GetId()}
	claimed, err := client.ClaimInboxItem(context.Background(), runtimeRequest(f.token, claimRequest))
	if err != nil || claimed.Msg.GetItem().GetState() != inboxv1.InboxState_INBOX_STATE_CLAIMED {
		t.Fatalf("claim = %+v, %v", claimed, err)
	}
	f.current = f.current.Add(time.Second)
	replayedClaim, err := client.ClaimInboxItem(context.Background(), runtimeRequest(f.token, claimRequest))
	if err != nil || !proto.Equal(claimed.Msg, replayedClaim.Msg) {
		t.Fatalf("claim replay = %+v, %v", replayedClaim, err)
	}
	if _, err := client.CompleteInboxItem(context.Background(), runtimeRequest(f.token, &inboxv1.CompleteInboxItemRequest{
		RequestId: claimRequestID, InboxItemId: item.GetId(),
	})); connectCode(err) != connect.CodeAlreadyExists {
		t.Fatalf("cross-operation request code = %v, error = %v", connectCode(err), err)
	}
	observed, err := client.ObserveTarget(context.Background(), runtimeRequest(f.token, &inboxv1.ObserveTargetRequest{
		Target: item.GetTarget(), Limit: 20,
	}))
	if err != nil || observed.Msg.GetHeadSequence() != item.GetTriggerTargetSequence() || len(observed.Msg.GetMessages()) != 1 ||
		len(observed.Msg.GetMessages()[0].GetMentionedPrincipals()) != 1 ||
		observed.Msg.GetMessages()[0].GetMentionedPrincipals()[0].GetId() != f.agent.ID {
		t.Fatalf("observe = %+v, %v", observed, err)
	}
	f.current = f.current.Add(time.Second)
	if _, err := f.database.SendMessage(context.Background(), store.SendMessageParams{
		RequestID: uuid.NewString(), Actor: f.owner,
		Target: store.MessageTarget{Kind: store.MessageTargetSpace, ID: f.group.ID},
		Body:   "advance", Now: f.current,
	}); err != nil {
		t.Fatal(err)
	}
	f.current = f.current.Add(time.Second)
	sendRequestID := uuid.NewString()
	sendRequest := &inboxv1.SendInboxReplyRequest{
		RequestId: sendRequestID, InboxItemId: item.GetId(), BasisTargetSequence: observed.Msg.GetHeadSequence(),
		Body: "held response", MentionedPrincipals: []*spacev1.Principal{{Kind: spacev1.PrincipalKind_PRINCIPAL_KIND_AGENT, Id: f.agent.ID}},
	}
	held, err := client.SendInboxReply(context.Background(), runtimeRequest(f.token, sendRequest))
	if err != nil || held.Msg.GetHeldDraft() == nil || held.Msg.GetMessage() != nil {
		t.Fatalf("held send = %+v, %v", held, err)
	}
	if held.Msg.GetHeldDraft().GetSequence() == 0 || held.Msg.GetHeldDraft().GetBasisTargetSequence() != observed.Msg.GetHeadSequence() ||
		len(held.Msg.GetHeldDraft().GetMentionedPrincipals()) != 1 ||
		held.Msg.GetHeldDraft().GetMentionedPrincipals()[0].GetId() != f.agent.ID {
		t.Fatalf("held mapping = %+v", held.Msg.GetHeldDraft())
	}
	f.current = f.current.Add(time.Second)
	replayedHeld, err := client.SendInboxReply(context.Background(), runtimeRequest(f.token, sendRequest))
	if err != nil || !proto.Equal(held.Msg, replayedHeld.Msg) {
		t.Fatalf("held replay = %+v, %v", replayedHeld, err)
	}
	drafts, err := client.ListHeldDrafts(context.Background(), runtimeRequest(f.token, &inboxv1.ListHeldDraftsRequest{Limit: 1}))
	if err != nil || len(drafts.Msg.GetDrafts()) != 1 || drafts.Msg.GetNextSequence() != held.Msg.GetHeldDraft().GetSequence() {
		t.Fatalf("held list = %+v, %v", drafts, err)
	}
	for _, limit := range []uint32{0, 201} {
		if _, err := client.ListHeldDrafts(context.Background(), runtimeRequest(f.token, &inboxv1.ListHeldDraftsRequest{Limit: limit})); connectCode(err) != connect.CodeInvalidArgument {
			t.Fatalf("held limit %d code = %v, error = %v", limit, connectCode(err), err)
		}
	}
	resolveRequestID := uuid.NewString()
	resolveRequest := &inboxv1.ResolveHeldDraftRequest{
		RequestId: resolveRequestID, HeldDraftId: held.Msg.GetHeldDraft().GetId(),
		Action: inboxv1.DraftResolutionAction_DRAFT_RESOLUTION_ACTION_CANCEL,
	}
	resolved, err := client.ResolveHeldDraft(context.Background(), runtimeRequest(f.token, resolveRequest))
	if err != nil || resolved.Msg.GetAction() != inboxv1.DraftResolutionAction_DRAFT_RESOLUTION_ACTION_CANCEL ||
		resolved.Msg.GetItem().GetCompletion() != inboxv1.InboxCompletion_INBOX_COMPLETION_CANCELLED || resolved.Msg.GetResult() != nil {
		t.Fatalf("resolve cancel = %+v, %v", resolved, err)
	}
	f.current = f.current.Add(time.Second)
	replayedResolved, err := client.ResolveHeldDraft(context.Background(), runtimeRequest(f.token, resolveRequest))
	if err != nil || !proto.Equal(resolved.Msg, replayedResolved.Msg) {
		t.Fatalf("resolve replay = %+v, %v", replayedResolved, err)
	}
	if _, err := client.ClaimInboxItem(context.Background(), runtimeRequest(f.token, &inboxv1.ClaimInboxItemRequest{
		RequestId: uuid.NewString(), InboxItemId: item.GetId(),
	})); connectCode(err) != connect.CodeFailedPrecondition {
		t.Fatalf("terminal claim code = %v, error = %v", connectCode(err), err)
	}
	f.current = f.current.Add(time.Second)
	secondTrigger, err := f.database.SendMessage(context.Background(), store.SendMessageParams{
		RequestID: uuid.NewString(), Actor: f.owner,
		Target: store.MessageTarget{Kind: store.MessageTargetSpace, ID: f.group.ID}, Body: "complete silently",
		MentionedPrincipals: []store.Principal{{Kind: store.PrincipalAgent, ID: f.agent.ID, OrganizationID: f.owner.OrganizationID}}, Now: f.current,
	})
	if err != nil {
		t.Fatal(err)
	}
	secondList, err := client.ListInboxItems(context.Background(), runtimeRequest(f.token, &inboxv1.ListInboxItemsRequest{Limit: 20}))
	if err != nil {
		t.Fatal(err)
	}
	var secondItem *inboxv1.InboxItem
	for _, candidate := range secondList.Msg.GetItems() {
		if candidate.GetTriggerMessageId() == secondTrigger.ID {
			secondItem = candidate
			break
		}
	}
	if secondItem == nil {
		t.Fatal("second inbox item not found")
	}
	if _, err := client.ClaimInboxItem(context.Background(), runtimeRequest(f.token, &inboxv1.ClaimInboxItemRequest{
		RequestId: uuid.NewString(), InboxItemId: secondItem.GetId(),
	})); err != nil {
		t.Fatal(err)
	}
	completeRequest := &inboxv1.CompleteInboxItemRequest{RequestId: uuid.NewString(), InboxItemId: secondItem.GetId()}
	completed, err := client.CompleteInboxItem(context.Background(), runtimeRequest(f.token, completeRequest))
	if err != nil || completed.Msg.GetItem().GetCompletion() != inboxv1.InboxCompletion_INBOX_COMPLETION_SILENT {
		t.Fatalf("complete = %+v, %v", completed, err)
	}
	f.current = f.current.Add(time.Second)
	replayedComplete, err := client.CompleteInboxItem(context.Background(), runtimeRequest(f.token, completeRequest))
	if err != nil || !proto.Equal(completed.Msg, replayedComplete.Msg) {
		t.Fatalf("complete replay = %+v, %v", replayedComplete, err)
	}
}

func TestInboxServiceErrorCodes(t *testing.T) {
	tests := map[connect.Code]error{
		connect.CodeUnauthenticated:    store.ErrAgentRuntimeUnauthenticated,
		connect.CodePermissionDenied:   store.ErrInboxAccessLost,
		connect.CodeNotFound:           store.ErrInboxItemNotFound,
		connect.CodeAlreadyExists:      store.ErrInboxRequestConflict,
		connect.CodeFailedPrecondition: store.ErrInboxBasisMismatch,
		connect.CodeInvalidArgument:    store.ErrInvalidMention,
		connect.CodeInternal:           errors.New("unexpected"),
	}
	for want, input := range tests {
		if got := connectCode(serviceError(input)); got != want {
			t.Fatalf("error %v code = %v, want %v", input, got, want)
		}
	}
}

func TestHeldDraftResultRefMappingIsExclusive(t *testing.T) {
	now := time.Now().UTC()
	messageID := uuid.NewString()
	successorID := uuid.NewString()
	tests := map[string]struct {
		draft store.HeldDraft
		kind  string
		id    string
	}{
		"fresh message": {
			draft: store.HeldDraft{State: store.HeldDraftStateSent, ResolutionAction: store.DraftResolutionRetry, ResultKind: store.InboxResultMessage, ResultID: messageID, CreatedAt: now, UpdatedAt: now},
			kind:  store.InboxResultMessage, id: messageID,
		},
		"successor held draft": {
			draft: store.HeldDraft{State: store.HeldDraftStateSuperseded, ResolutionAction: store.DraftResolutionRetry, ResultKind: store.InboxResultHeldDraft, ResultID: successorID, CreatedAt: now, UpdatedAt: now},
			kind:  store.InboxResultHeldDraft, id: successorID,
		},
		"held no result": {
			draft: store.HeldDraft{State: store.HeldDraftStateHeld, CreatedAt: now, UpdatedAt: now},
		},
		"cancelled no result": {
			draft: store.HeldDraft{State: store.HeldDraftStateCancelled, ResolutionAction: store.DraftResolutionCancel, CreatedAt: now, UpdatedAt: now},
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			spaceID := uuid.NewString()
			test.draft.ID = uuid.NewString()
			test.draft.InboxItemID = uuid.NewString()
			test.draft.SpaceID = spaceID
			test.draft.Target = store.MessageTarget{Kind: store.MessageTargetSpace, ID: spaceID}
			test.draft.Body = "draft body"
			message, err := messagecodec.HeldDraft(test.draft)
			if err != nil {
				t.Fatal(err)
			}
			switch test.kind {
			case store.InboxResultMessage:
				if _, ok := message.GetResultRef().(*inboxv1.HeldDraft_ResultMessageId); !ok || message.GetResultMessageId() != test.id || message.GetResultHeldDraftId() != "" {
					t.Fatalf("message result ref = %+v", message.GetResultRef())
				}
			case store.InboxResultHeldDraft:
				if _, ok := message.GetResultRef().(*inboxv1.HeldDraft_ResultHeldDraftId); !ok || message.GetResultHeldDraftId() != test.id || message.GetResultMessageId() != "" {
					t.Fatalf("held result ref = %+v", message.GetResultRef())
				}
			default:
				if message.GetResultRef() != nil || message.GetResultMessageId() != "" || message.GetResultHeldDraftId() != "" {
					t.Fatalf("unset result ref = %+v", message.GetResultRef())
				}
			}
		})
	}
}

type serviceFixture struct {
	database *store.Store
	owner    store.Principal
	agent    store.Agent
	group    store.Space
	trigger  store.Message
	token    string
	current  time.Time
}

func openServiceFixture(t *testing.T) *serviceFixture {
	t.Helper()
	database, err := store.Open(filepath.Join(t.TempDir(), "server.db"))
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
	bootstrap, err := database.EnsureAuthority(ctx, "inbox-service-owner-credential-abcdefghijklmnopqrstuvwxyz", base)
	if err != nil {
		t.Fatal(err)
	}
	owner := store.Principal{Kind: "human", ID: bootstrap.Human.ID, OrganizationID: bootstrap.Organization.ID}
	agent, err := database.CreateAgent(ctx, store.CreateAgentParams{
		RequestID: uuid.NewString(), Actor: owner, Handle: "inbox-service", DisplayName: "Inbox Service", Role: "worker", Mission: "Process inbox tests", Now: base.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	registrationKey := "inbox-service-computer-registration-key"
	pairingToken := base64.RawURLEncoding.EncodeToString([]byte("abcdef0123456789abcdef0123456789"))
	if _, err := database.CreateComputerPairing(ctx, store.CreateComputerPairingParams{
		RequestID: uuid.NewString(), Actor: owner, Token: pairingToken,
		ExpiresAt: base.Add(3 * time.Minute), Now: base.Add(2 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	computer, err := database.PairComputer(ctx, store.PairComputerParams{
		RequestID: uuid.NewString(), PairingToken: pairingToken,
		RegistrationKey: registrationKey, Name: "inbox-host", OS: "linux", Arch: "arm64",
		CapabilityInventory: inboxTestCapabilityInventory(), Now: base.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	bindingHandle := completeInboxServiceCredential(t, database, owner, computer.ID, registrationKey, agent.ID, base.Add(2*time.Second))
	if _, err := database.UpdateAgentRuntimeSpec(ctx, store.UpdateAgentRuntimeSpecParams{
		RequestID: uuid.NewString(), Actor: owner, AgentID: agent.ID,
		Engine: agentapp.EngineBuiltin, ProviderProtocol: agentapp.ProviderOpenAIResponses,
		ProviderEndpoint: "https://provider.invalid/v1", Model: "test-model", CredentialBindingHandle: bindingHandle,
		SandboxProvider: "trusted_local", MaxRunDuration: 2 * time.Minute, MaxOutputBytes: 1 << 20,
		ToolPolicy: agentapp.ToolPolicy{Message: true}, Now: base.Add(2 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	placement, err := database.SetAgentPlacement(ctx, store.SetAgentPlacementParams{
		RequestID: uuid.NewString(), Actor: owner, AgentID: agent.ID, ComputerID: computer.ID, Now: base.Add(3 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.AcknowledgeAgentPlacement(ctx, store.AcknowledgePlacementParams{
		ComputerID: computer.ID, RegistrationKey: registrationKey, AgentID: agent.ID,
		DesiredRevision: placement.DesiredRevision, State: "ready", Now: base.Add(4 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	token := base64.RawURLEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	if _, err := database.CreateAgentRuntimeSession(ctx, store.CreateAgentRuntimeSessionParams{
		ComputerID: computer.ID, RegistrationKey: registrationKey, AgentID: agent.ID,
		PlacementDesiredRevision: placement.DesiredRevision, Token: token,
		Now: base.Add(5 * time.Second), ExpiresAt: base.Add(10*time.Minute + 5*time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	group, err := database.CreateGroup(ctx, store.CreateGroupParams{
		RequestID: uuid.NewString(), Actor: owner, Name: "Inbox Service", Now: base.Add(6 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.AddMember(ctx, store.ChangeMemberParams{
		RequestID: uuid.NewString(), Actor: owner, SpaceID: group.ID,
		Member: store.Principal{Kind: "agent", ID: agent.ID}, Now: base.Add(7 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	for index, capability := range []store.Capability{store.CapabilitySpaceRead, store.CapabilityMessageSend} {
		if _, err := database.IssueGrant(ctx, store.IssueGrantParams{
			RequestID: uuid.NewString(), Actor: owner,
			Subject: store.Principal{Kind: "agent", ID: agent.ID}, Capability: capability,
			Scope: store.Scope{Kind: "space", ID: group.ID}, ParentGrantID: bootstrap.RootGrant.ID,
			Now: base.Add(time.Duration(8+index) * time.Second),
		}); err != nil {
			t.Fatal(err)
		}
	}
	trigger, err := database.SendMessage(ctx, store.SendMessageParams{
		RequestID: uuid.NewString(), Actor: owner,
		Target: store.MessageTarget{Kind: store.MessageTargetSpace, ID: group.ID}, Body: "trigger",
		MentionedPrincipals: []store.Principal{{Kind: store.PrincipalAgent, ID: agent.ID, OrganizationID: owner.OrganizationID}}, Now: base.Add(10 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.SendMessage(ctx, store.SendMessageParams{
		RequestID: uuid.NewString(), Actor: owner,
		Target: store.MessageTarget{Kind: store.MessageTargetThread, ID: trigger.ID}, Body: "create thread",
		Now: base.Add(11 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	return &serviceFixture{
		database: database, owner: owner, agent: agent, group: group, trigger: trigger,
		token: token, current: base.Add(12 * time.Second),
	}
}

func inboxTestCapabilityInventory() computerdomain.CapabilityInventory {
	inventory := computerdomain.TrustedLocalCapabilityInventory(computerdomain.EngineCapability{
		Kind: computerdomain.EngineBuiltin, Version: "test", ProtocolVersion: 1,
		SupportsToolCalls: true, SupportsCancel: true, OpenAIResponses: true, Healthy: true,
	})
	inventory.CredentialDelivery = computerdomain.CredentialDeliveryCapability{
		Healthy: true, Algorithm: "x25519_xchacha20_poly1305", Store: "linux_secret_service",
		KeyID: "inbox-service-key", PublicKey: base64.RawURLEncoding.EncodeToString(make([]byte, 32)),
	}
	return inventory
}

func completeInboxServiceCredential(
	t *testing.T,
	database *store.Store,
	owner store.Principal,
	computerID, registrationKey, agentID string,
	now time.Time,
) string {
	t.Helper()
	delivery, err := database.EnqueueCredentialDelivery(context.Background(), computerapp.EnqueueCredentialDeliveryCommand{
		RequestID: uuid.NewString(), Actor: owner, ComputerID: computerID, AgentID: agentID, CredentialKind: "openai",
		Sealed: computerapp.SealedCredential{
			Algorithm: "x25519_xchacha20_poly1305", KeyID: "inbox-service-key",
			EphemeralPublicKey: make([]byte, 32), Nonce: make([]byte, 24), Ciphertext: make([]byte, 17),
		},
		ExpiresAt: now.Add(5 * time.Minute), Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := database.ClaimCredentialDelivery(context.Background(), computerapp.ClaimCredentialDeliveryCommand{
		ComputerID: computerID, RegistrationKey: registrationKey, Now: now,
	})
	if err != nil || claimed.ID != delivery.ID {
		t.Fatalf("credential claim = %+v, %v", claimed, err)
	}
	handle := "cred_inbox_service_" + agentID
	completed, err := database.CompleteCredentialDelivery(context.Background(), computerapp.CompleteCredentialDeliveryCommand{
		ComputerID: computerID, RegistrationKey: registrationKey, DeliveryID: delivery.ID, BindingHandle: handle, Now: now,
	})
	if err != nil || completed.State != computerapp.CredentialDeliverySucceeded {
		t.Fatalf("credential completion = %+v, %v", completed, err)
	}
	return handle
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
