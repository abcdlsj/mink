package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	inboxv1 "github.com/abcdlsj/sumi/gen/go/sumi/inbox/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/inbox/v1/inboxv1connect"
	"github.com/abcdlsj/sumi/gen/go/sumi/runtime/v1/runtimev1connect"
	spacev1 "github.com/abcdlsj/sumi/gen/go/sumi/space/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/space/v1/spacev1connect"
	"github.com/abcdlsj/sumi/internal/authority"
	grantapp "github.com/abcdlsj/sumi/internal/grant/application"
	"github.com/abcdlsj/sumi/internal/store"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
)

func TestInboxHTTPCommonProceduresAcceptHumanOrCurrentAgentAndAgentAdaptersRequireRuntime(t *testing.T) {
	dataRoot := t.TempDir()
	api := openFactsAPI(t, dataRoot)
	defer api.close(t)
	computer, agent, placement, registrationKey := createActiveRuntimeBinding(t, api)
	runtimeClient := runtimev1connect.NewAgentRuntimeServiceClient(api.http.Client(), api.http.URL)
	oldSession := createRuntimeOverHTTP(t, runtimeClient, computer.GetId(), registrationKey, agent.GetId(), placement.GetDesiredRevision())
	currentSession := createRuntimeOverHTTP(t, runtimeClient, computer.GetId(), registrationKey, agent.GetId(), placement.GetDesiredRevision())
	ownerCredential, err := authority.ReadCredentialFile(filepath.Join(dataRoot, "owner.key"))
	if err != nil {
		t.Fatal(err)
	}
	client := inboxv1connect.NewInboxServiceClient(api.http.Client(), api.http.URL)
	itemID, draftID, spaceID, threadID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	requestID := uuid.NewString()
	commonCalls := map[string]func(string) error{
		"notice": func(token string) error {
			_, err := client.GetInboxNotice(context.Background(), inboxRequest(token, &inboxv1.GetInboxNoticeRequest{}))
			return err
		},
		"list items": func(token string) error {
			_, err := client.ListInboxItems(context.Background(), inboxRequest(token, &inboxv1.ListInboxItemsRequest{Limit: 1}))
			return err
		},
		"claim": func(token string) error {
			_, err := client.ClaimInboxItem(context.Background(), inboxRequest(token, &inboxv1.ClaimInboxItemRequest{RequestId: requestID, InboxItemId: itemID}))
			return err
		},
		"observe": func(token string) error {
			_, err := client.ObserveTarget(context.Background(), inboxRequest(token, &inboxv1.ObserveTargetRequest{
				Target: spaceTarget(spaceID), Limit: 1,
			}))
			return err
		},
		"complete": func(token string) error {
			_, err := client.CompleteInboxItem(context.Background(), inboxRequest(token, &inboxv1.CompleteInboxItemRequest{RequestId: requestID, InboxItemId: itemID}))
			return err
		},
		"mute": func(token string) error {
			_, err := client.SetSpaceMute(context.Background(), inboxRequest(token, &inboxv1.SetSpaceMuteRequest{RequestId: requestID, SpaceId: spaceID, Muted: true}))
			return err
		},
		"follow": func(token string) error {
			_, err := client.SetThreadFollow(context.Background(), inboxRequest(token, &inboxv1.SetThreadFollowRequest{RequestId: requestID, ThreadRootMessageId: threadID, Followed: true}))
			return err
		},
	}
	agentCalls := map[string]func(string) error{
		"send": func(token string) error {
			_, err := client.SendInboxReply(context.Background(), inboxRequest(token, &inboxv1.SendInboxReplyRequest{RequestId: requestID, InboxItemId: itemID, Body: "reply"}))
			return err
		},
		"list drafts": func(token string) error {
			_, err := client.ListHeldDrafts(context.Background(), inboxRequest(token, &inboxv1.ListHeldDraftsRequest{Limit: 1}))
			return err
		},
		"resolve": func(token string) error {
			_, err := client.ResolveHeldDraft(context.Background(), inboxRequest(token, &inboxv1.ResolveHeldDraftRequest{
				RequestId: requestID, HeldDraftId: draftID,
				Action: inboxv1.DraftResolutionAction_DRAFT_RESOLUTION_ACTION_CANCEL,
			}))
			return err
		},
	}
	for name, call := range commonCalls {
		for credentialName, token := range map[string]string{"missing": "", "old runtime": oldSession.GetToken()} {
			t.Run(name+"/"+credentialName, func(t *testing.T) {
				assertConnectCode(t, call(token), connect.CodeUnauthenticated)
			})
		}
		for credentialName, token := range map[string]string{"human": ownerCredential, "current runtime": currentSession.GetToken()} {
			t.Run(name+"/"+credentialName, func(t *testing.T) {
				if err := call(token); connect.CodeOf(err) == connect.CodeUnauthenticated {
					t.Fatalf("valid principal rejected before service: %v", err)
				}
			})
		}
	}
	for name, call := range agentCalls {
		for credentialName, token := range map[string]string{
			"missing": "", "human": ownerCredential, "old runtime": oldSession.GetToken(),
		} {
			t.Run(name+"/"+credentialName, func(t *testing.T) {
				assertConnectCode(t, call(token), connect.CodeUnauthenticated)
			})
		}
		t.Run(name+"/current runtime", func(t *testing.T) {
			if err := call(currentSession.GetToken()); connect.CodeOf(err) == connect.CodeUnauthenticated {
				t.Fatalf("current runtime rejected before service: %v", err)
			}
		})
	}
}

func TestInboxBrowserSessionAuthenticatesHumanCommonSurface(t *testing.T) {
	dataRoot := t.TempDir()
	api := openBrowserServer(t, dataRoot)
	defer api.close(t)
	ownerCredential, err := authority.ReadCredentialFile(filepath.Join(dataRoot, "owner.key"))
	if err != nil {
		t.Fatal(err)
	}
	bootstrap, err := api.app.store.EnsureAuthority(context.Background(), ownerCredential, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	owner := store.Principal{Kind: "human", ID: bootstrap.Human.ID, OrganizationID: bootstrap.Organization.ID}
	memberCredential := "browser-inbox-member-credential-abcdefghijklmnopqrstuvwxyz"
	member, err := api.app.store.CreateHuman(context.Background(), store.CreateHumanParams{
		RequestID: uuid.NewString(), Actor: owner, Name: "Browser Inbox Member", Role: "member",
		Credential: memberCredential, Now: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	ownerBrowser := browserClient(t, api.origin, ownerCredential)
	collaboration := spacev1connect.NewCollaborationServiceClient(ownerBrowser, api.origin, originAuthorization(api.origin))
	groupResponse, err := collaboration.CreateGroup(context.Background(), connect.NewRequest(&spacev1.CreateGroupRequest{
		RequestId: uuid.NewString(), Name: "Browser Human Inbox",
	}))
	if err != nil {
		t.Fatal(err)
	}
	group := groupResponse.Msg.GetSpace()
	memberPrincipal := &spacev1.Principal{Kind: spacev1.PrincipalKind_PRINCIPAL_KIND_HUMAN, Id: member.ID}
	if _, err := collaboration.AddMember(context.Background(), connect.NewRequest(&spacev1.AddMemberRequest{
		RequestId: uuid.NewString(), SpaceId: group.GetId(), Member: memberPrincipal,
	})); err != nil {
		t.Fatal(err)
	}
	if _, err := api.app.store.IssueGrant(context.Background(), store.IssueGrantParams{
		RequestID: uuid.NewString(), Actor: owner,
		Subject:    store.Principal{Kind: "human", ID: member.ID, OrganizationID: owner.OrganizationID},
		Capability: store.CapabilitySpaceRead, Scope: store.Scope{Kind: "space", ID: group.GetId()},
		ParentGrantID: bootstrap.RootGrant.ID, Now: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	triggerResponse, err := collaboration.SendMessage(context.Background(), connect.NewRequest(&spacev1.SendMessageRequest{
		RequestId: uuid.NewString(), Target: spaceTarget(group.GetId()), Body: "human inbox trigger",
		MentionedPrincipals: []*spacev1.Principal{memberPrincipal},
	}))
	if err != nil {
		t.Fatal(err)
	}
	trigger := triggerResponse.Msg.GetMessage()
	if _, err := collaboration.SendMessage(context.Background(), connect.NewRequest(&spacev1.SendMessageRequest{
		RequestId: uuid.NewString(),
		Target:    &spacev1.MessageTarget{Target: &spacev1.MessageTarget_ThreadRootMessageId{ThreadRootMessageId: trigger.GetId()}},
		Body:      "thread exists for browser follow",
	})); err != nil {
		t.Fatal(err)
	}

	memberBrowser := browserClient(t, api.origin, memberCredential)
	readClient := inboxv1connect.NewInboxServiceClient(memberBrowser, api.origin)
	notice, err := readClient.GetInboxNotice(context.Background(), connect.NewRequest(&inboxv1.GetInboxNoticeRequest{}))
	if err != nil || !notice.Msg.GetHasUnread() {
		t.Fatalf("browser inbox notice = %+v, %v", notice, err)
	}
	listed, err := readClient.ListInboxItems(context.Background(), connect.NewRequest(&inboxv1.ListInboxItemsRequest{Limit: 10}))
	if err != nil || len(listed.Msg.GetItems()) != 1 {
		t.Fatalf("browser inbox items = %+v, %v", listed, err)
	}
	item := listed.Msg.GetItems()[0]
	if item.GetTriggerMessageId() != trigger.GetId() || item.GetRecipient().GetKind() != spacev1.PrincipalKind_PRINCIPAL_KIND_HUMAN || item.GetRecipient().GetId() != member.ID {
		t.Fatalf("browser inbox item lost Human identity or canonical trigger: %+v", item)
	}
	workAttention := inboxv1connect.NewWorkAttentionServiceClient(memberBrowser, api.origin)
	attention, err := workAttention.ListWorkAttentionItems(context.Background(), connect.NewRequest(&inboxv1.ListWorkAttentionItemsRequest{Limit: 10}))
	if err != nil || len(attention.Msg.GetItems()) != 0 {
		t.Fatalf("independent Work Attention projection = %+v, %v", attention, err)
	}

	missingOrigin := inboxv1connect.NewInboxServiceClient(memberBrowser, api.origin)
	wrongOrigin := inboxv1connect.NewInboxServiceClient(memberBrowser, api.origin, originAuthorization("http://localhost:18080"))
	validMutation := inboxv1connect.NewInboxServiceClient(memberBrowser, api.origin, originAuthorization(api.origin))
	for name, client := range map[string]inboxv1connect.InboxServiceClient{
		"missing origin": missingOrigin,
		"wrong origin":   wrongOrigin,
	} {
		t.Run(name, func(t *testing.T) {
			mutations := map[string]func() error{
				"observe": func() error {
					_, err := client.ObserveTarget(context.Background(), connect.NewRequest(&inboxv1.ObserveTargetRequest{
						Target: spaceTarget(group.GetId()), Limit: 10,
					}))
					return err
				},
				"claim": func() error {
					_, err := client.ClaimInboxItem(context.Background(), connect.NewRequest(&inboxv1.ClaimInboxItemRequest{
						RequestId: uuid.NewString(), InboxItemId: item.GetId(),
					}))
					return err
				},
				"complete": func() error {
					_, err := client.CompleteInboxItem(context.Background(), connect.NewRequest(&inboxv1.CompleteInboxItemRequest{
						RequestId: uuid.NewString(), InboxItemId: item.GetId(),
					}))
					return err
				},
				"mute": func() error {
					_, err := client.SetSpaceMute(context.Background(), connect.NewRequest(&inboxv1.SetSpaceMuteRequest{
						RequestId: uuid.NewString(), SpaceId: group.GetId(), Muted: true,
					}))
					return err
				},
				"follow": func() error {
					_, err := client.SetThreadFollow(context.Background(), connect.NewRequest(&inboxv1.SetThreadFollowRequest{
						RequestId: uuid.NewString(), ThreadRootMessageId: trigger.GetId(), Followed: true,
					}))
					return err
				},
			}
			for operation, mutate := range mutations {
				t.Run(operation, func(t *testing.T) {
					assertConnectCode(t, mutate(), connect.CodePermissionDenied)
				})
			}
		})
	}
	listed, err = readClient.ListInboxItems(context.Background(), connect.NewRequest(&inboxv1.ListInboxItemsRequest{Limit: 10}))
	if err != nil || listed.Msg.GetItems()[0].GetState() != inboxv1.InboxState_INBOX_STATE_UNREAD {
		t.Fatalf("rejected browser mutation changed item = %+v, %v", listed, err)
	}
	observed, err := validMutation.ObserveTarget(context.Background(), connect.NewRequest(&inboxv1.ObserveTargetRequest{
		Target: spaceTarget(group.GetId()), Limit: 10,
	}))
	if err != nil || len(observed.Msg.GetMessages()) != 1 || observed.Msg.GetMessages()[0].GetId() != trigger.GetId() {
		t.Fatalf("same-origin browser observation = %+v, %v", observed, err)
	}
	claimed, err := validMutation.ClaimInboxItem(context.Background(), connect.NewRequest(&inboxv1.ClaimInboxItemRequest{
		RequestId: uuid.NewString(), InboxItemId: item.GetId(),
	}))
	if err != nil || claimed.Msg.GetItem().GetState() != inboxv1.InboxState_INBOX_STATE_CLAIMED {
		t.Fatalf("same-origin browser claim = %+v, %v", claimed, err)
	}
	muted, err := validMutation.SetSpaceMute(context.Background(), connect.NewRequest(&inboxv1.SetSpaceMuteRequest{
		RequestId: uuid.NewString(), SpaceId: group.GetId(), Muted: true,
	}))
	if err != nil || !muted.Msg.GetMuted() {
		t.Fatalf("same-origin browser mute = %+v, %v", muted, err)
	}
	followed, err := validMutation.SetThreadFollow(context.Background(), connect.NewRequest(&inboxv1.SetThreadFollowRequest{
		RequestId: uuid.NewString(), ThreadRootMessageId: trigger.GetId(), Followed: true,
	}))
	if err != nil || !followed.Msg.GetFollowed() {
		t.Fatalf("same-origin browser follow = %+v, %v", followed, err)
	}
	completed, err := validMutation.CompleteInboxItem(context.Background(), connect.NewRequest(&inboxv1.CompleteInboxItemRequest{
		RequestId: uuid.NewString(), InboxItemId: item.GetId(),
	}))
	if err != nil || completed.Msg.GetItem().GetState() != inboxv1.InboxState_INBOX_STATE_DONE || completed.Msg.GetItem().GetCompletion() != inboxv1.InboxCompletion_INBOX_COMPLETION_SILENT {
		t.Fatalf("same-origin browser completion = %+v, %v", completed, err)
	}
	_, err = readClient.ListHeldDrafts(context.Background(), connect.NewRequest(&inboxv1.ListHeldDraftsRequest{Limit: 1}))
	assertConnectCode(t, err, connect.CodeUnauthenticated)
}

func TestAgentInboxHTTPFreshHeldResolveAndRestartReplay(t *testing.T) {
	dataRoot := t.TempDir()
	api := openFactsAPI(t, dataRoot)
	computer, agent, placement, registrationKey := createActiveRuntimeBinding(t, api)
	runtimeClient := runtimev1connect.NewAgentRuntimeServiceClient(api.http.Client(), api.http.URL)
	session := createRuntimeOverHTTP(t, runtimeClient, computer.GetId(), registrationKey, agent.GetId(), placement.GetDesiredRevision())
	group := createInboxSpace(t, api, dataRoot, agent.GetId())
	humanClient := spacev1connect.NewCollaborationServiceClient(api.http.Client(), api.http.URL, ownerClientAuthorization(t, dataRoot))
	inboxClient := inboxv1connect.NewInboxServiceClient(api.http.Client(), api.http.URL)

	freshTrigger := sendMention(t, humanClient, group.GetId(), agent.GetId(), "fresh trigger")
	freshItem := findInboxItem(t, inboxClient, session.GetToken(), freshTrigger.GetId())
	claimInbox(t, inboxClient, session.GetToken(), freshItem.GetId())
	freshObserved := observeInbox(t, inboxClient, session.GetToken(), freshItem.GetTarget())
	freshRequest := &inboxv1.SendInboxReplyRequest{
		RequestId: uuid.NewString(), InboxItemId: freshItem.GetId(),
		BasisTargetSequence: freshObserved.GetHeadSequence(), Body: "fresh-body-secret",
	}
	freshResponse, err := inboxClient.SendInboxReply(context.Background(), inboxRequest(session.GetToken(), freshRequest))
	if err != nil || freshResponse.Msg.GetMessage() == nil || freshResponse.Msg.GetHeldDraft() != nil {
		t.Fatalf("fresh response = %+v, %v", freshResponse, err)
	}
	conflictBody := "http-error-body-secret"
	conflictRequest := proto.Clone(freshRequest).(*inboxv1.SendInboxReplyRequest)
	conflictRequest.Body = conflictBody
	_, err = inboxClient.SendInboxReply(context.Background(), inboxRequest(session.GetToken(), conflictRequest))
	assertConnectCode(t, err, connect.CodeAlreadyExists)
	for _, privateValue := range []string{freshRequest.GetBody(), conflictBody, session.GetToken(), registrationKey} {
		if strings.Contains(err.Error(), privateValue) {
			t.Fatalf("inbox conflict error contains private value %q: %v", privateValue, err)
		}
	}

	heldTrigger := sendMention(t, humanClient, group.GetId(), agent.GetId(), "held trigger")
	heldItem := findInboxItem(t, inboxClient, session.GetToken(), heldTrigger.GetId())
	claimInbox(t, inboxClient, session.GetToken(), heldItem.GetId())
	heldObserved := observeInbox(t, inboxClient, session.GetToken(), heldItem.GetTarget())
	if _, err := humanClient.SendMessage(context.Background(), connect.NewRequest(&spacev1.SendMessageRequest{
		RequestId: uuid.NewString(), Target: spaceTarget(group.GetId()), Body: "advance-body-secret",
	})); err != nil {
		t.Fatal(err)
	}
	heldRequest := &inboxv1.SendInboxReplyRequest{
		RequestId: uuid.NewString(), InboxItemId: heldItem.GetId(),
		BasisTargetSequence: heldObserved.GetHeadSequence(), Body: "held-body-secret",
	}
	heldResponse, err := inboxClient.SendInboxReply(context.Background(), inboxRequest(session.GetToken(), heldRequest))
	if err != nil || heldResponse.Msg.GetHeldDraft() == nil || heldResponse.Msg.GetMessage() != nil {
		t.Fatalf("held response = %+v, %v", heldResponse, err)
	}

	api.close(t)
	api = openFactsAPI(t, dataRoot)
	inboxClient = inboxv1connect.NewInboxServiceClient(api.http.Client(), api.http.URL)
	replayedFresh, err := inboxClient.SendInboxReply(context.Background(), inboxRequest(session.GetToken(), freshRequest))
	if err != nil || !proto.Equal(freshResponse.Msg, replayedFresh.Msg) {
		t.Fatalf("fresh restart replay = %+v, %v", replayedFresh, err)
	}
	replayedHeld, err := inboxClient.SendInboxReply(context.Background(), inboxRequest(session.GetToken(), heldRequest))
	if err != nil || !proto.Equal(heldResponse.Msg, replayedHeld.Msg) {
		t.Fatalf("held restart replay = %+v, %v", replayedHeld, err)
	}
	drafts, err := inboxClient.ListHeldDrafts(context.Background(), inboxRequest(session.GetToken(), &inboxv1.ListHeldDraftsRequest{Limit: 1}))
	if err != nil || len(drafts.Msg.GetDrafts()) != 1 || drafts.Msg.GetDrafts()[0].GetId() != heldResponse.Msg.GetHeldDraft().GetId() {
		t.Fatalf("restart drafts = %+v, %v", drafts, err)
	}
	current := observeInbox(t, inboxClient, session.GetToken(), heldItem.GetTarget())
	resolveRequest := &inboxv1.ResolveHeldDraftRequest{
		RequestId: uuid.NewString(), HeldDraftId: heldResponse.Msg.GetHeldDraft().GetId(),
		Action:              inboxv1.DraftResolutionAction_DRAFT_RESOLUTION_ACTION_RETRY,
		BasisTargetSequence: current.GetHeadSequence(),
	}
	resolved, err := inboxClient.ResolveHeldDraft(context.Background(), inboxRequest(session.GetToken(), resolveRequest))
	if err != nil || resolved.Msg.GetMessage() == nil || resolved.Msg.GetItem().GetCompletion() != inboxv1.InboxCompletion_INBOX_COMPLETION_SENT {
		t.Fatalf("resolved = %+v, %v", resolved, err)
	}

	api.close(t)
	api = openFactsAPI(t, dataRoot)
	inboxClient = inboxv1connect.NewInboxServiceClient(api.http.Client(), api.http.URL)
	replayedResolve, err := inboxClient.ResolveHeldDraft(context.Background(), inboxRequest(session.GetToken(), resolveRequest))
	if err != nil || !proto.Equal(resolved.Msg, replayedResolve.Msg) {
		t.Fatalf("resolve restart replay = %+v, %v", replayedResolve, err)
	}
	if _, err := api.app.store.AuthenticateAgentRuntimeSession(context.Background(), session.GetToken(), time.Now()); err != nil {
		t.Fatal(err)
	}
	api.close(t)
	assertAgentInboxDataRootQuiet(t, dataRoot,
		[]string{freshRequest.GetBody(), heldRequest.GetBody(), conflictBody, "advance-body-secret"},
		[]string{session.GetToken(), registrationKey},
	)
}

func createInboxSpace(t *testing.T, api *factsAPI, dataRoot, agentID string) *spacev1.Space {
	t.Helper()
	credential, err := authority.ReadCredentialFile(filepath.Join(dataRoot, "owner.key"))
	if err != nil {
		t.Fatal(err)
	}
	owner, err := api.app.store.AuthenticateHuman(context.Background(), credential)
	if err != nil {
		t.Fatal(err)
	}
	grants, err := api.app.store.ListGrants(context.Background(), grantapp.ListQuery{OrganizationID: owner.OrganizationID})
	if err != nil {
		t.Fatal(err)
	}
	rootGrantID := ""
	for _, grant := range grants {
		if grant.ParentGrantID == "" && grant.Capability == store.CapabilityOrganizationAdmin {
			rootGrantID = grant.ID
			break
		}
	}
	if rootGrantID == "" {
		t.Fatal("root grant not found")
	}
	humanClient := spacev1connect.NewCollaborationServiceClient(api.http.Client(), api.http.URL, ownerClientAuthorization(t, dataRoot))
	groupResponse, err := humanClient.CreateGroup(context.Background(), connect.NewRequest(&spacev1.CreateGroupRequest{
		RequestId: uuid.NewString(), Name: "Agent Inbox Server",
	}))
	if err != nil {
		t.Fatal(err)
	}
	group := groupResponse.Msg.GetSpace()
	if _, err := humanClient.AddMember(context.Background(), connect.NewRequest(&spacev1.AddMemberRequest{
		RequestId: uuid.NewString(), SpaceId: group.GetId(),
		Member: &spacev1.Principal{Kind: spacev1.PrincipalKind_PRINCIPAL_KIND_AGENT, Id: agentID},
	})); err != nil {
		t.Fatal(err)
	}
	for _, capability := range []store.Capability{store.CapabilitySpaceRead, store.CapabilityMessageSend} {
		if _, err := api.app.store.IssueGrant(context.Background(), store.IssueGrantParams{
			RequestID: uuid.NewString(), Actor: owner,
			Subject:    store.Principal{Kind: "agent", ID: agentID, OrganizationID: owner.OrganizationID},
			Capability: capability, Scope: store.Scope{Kind: "space", ID: group.GetId()},
			ParentGrantID: rootGrantID, Now: time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	return group
}

func sendMention(t *testing.T, client spacev1connect.CollaborationServiceClient, spaceID, agentID, body string) *spacev1.Message {
	t.Helper()
	response, err := client.SendMessage(context.Background(), connect.NewRequest(&spacev1.SendMessageRequest{
		RequestId: uuid.NewString(), Target: spaceTarget(spaceID), Body: body,
		MentionedPrincipals: []*spacev1.Principal{{Kind: spacev1.PrincipalKind_PRINCIPAL_KIND_AGENT, Id: agentID}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	return response.Msg.GetMessage()
}

func findInboxItem(t *testing.T, client inboxv1connect.InboxServiceClient, token, triggerMessageID string) *inboxv1.InboxItem {
	t.Helper()
	response, err := client.ListInboxItems(context.Background(), inboxRequest(token, &inboxv1.ListInboxItemsRequest{Limit: 200}))
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range response.Msg.GetItems() {
		if item.GetTriggerMessageId() == triggerMessageID {
			return item
		}
	}
	t.Fatalf("inbox item for trigger %s not found", triggerMessageID)
	return nil
}

func claimInbox(t *testing.T, client inboxv1connect.InboxServiceClient, token, itemID string) {
	t.Helper()
	if _, err := client.ClaimInboxItem(context.Background(), inboxRequest(token, &inboxv1.ClaimInboxItemRequest{
		RequestId: uuid.NewString(), InboxItemId: itemID,
	})); err != nil {
		t.Fatal(err)
	}
}

func observeInbox(t *testing.T, client inboxv1connect.InboxServiceClient, token string, target *spacev1.MessageTarget) *inboxv1.ObserveTargetResponse {
	t.Helper()
	response, err := client.ObserveTarget(context.Background(), inboxRequest(token, &inboxv1.ObserveTargetRequest{Target: target, Limit: 200}))
	if err != nil {
		t.Fatal(err)
	}
	return response.Msg
}

func inboxRequest[T any](token string, message *T) *connect.Request[T] {
	request := connect.NewRequest(message)
	if token != "" {
		request.Header().Set("Authorization", "Bearer "+token)
	}
	return request
}

func spaceTarget(spaceID string) *spacev1.MessageTarget {
	return &spacev1.MessageTarget{Target: &spacev1.MessageTarget_SpaceId{SpaceId: spaceID}}
}

func assertAgentInboxDataRootQuiet(t *testing.T, dataRoot string, businessValues, globalValues []string) {
	t.Helper()
	app, err := New(context.Background(), Config{DataRoot: dataRoot})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := app.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	database, err := sql.Open("sqlite", filepath.Join(dataRoot, "data", "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	waitForKnowledgeMessages(t, database)
	rows, err := database.Query(`SELECT name FROM sqlite_schema WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		tables = append(tables, table)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	for _, table := range tables {
		columns, err := database.Query(`PRAGMA table_info(` + quoteSQLiteIdentifier(table) + `)`)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for columns.Next() {
			var cid, notNull, primaryKey int
			var name, columnType string
			var defaultValue any
			if err := columns.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
				columns.Close()
				t.Fatal(err)
			}
			names = append(names, name)
		}
		if err := columns.Close(); err != nil {
			t.Fatal(err)
		}
		values := globalValues
		if table != "messages" && table != "agent_held_drafts" && table != "knowledge_fts" && !strings.HasPrefix(table, "knowledge_fts_") {
			values = append(append([]string(nil), values...), businessValues...)
		}
		for _, name := range names {
			for _, value := range values {
				var found bool
				query := fmt.Sprintf(`SELECT EXISTS(SELECT 1 FROM %s WHERE instr(CAST(%s AS TEXT), ?) > 0)`, quoteSQLiteIdentifier(table), quoteSQLiteIdentifier(name))
				if err := database.QueryRow(query, value).Scan(&found); err != nil {
					t.Fatal(err)
				}
				if found {
					t.Fatalf("private value %q persisted in %s.%s", value, table, name)
				}
			}
		}
	}
	for _, value := range businessValues {
		var canonical bool
		if err := database.QueryRow(`SELECT EXISTS(SELECT 1 FROM messages WHERE body = ?)`, value).Scan(&canonical); err != nil {
			t.Fatal(err)
		}
		var projected bool
		if err := database.QueryRow(`SELECT EXISTS(SELECT 1 FROM knowledge_fts WHERE instr(body, ?) > 0)`, value).Scan(&projected); err != nil {
			t.Fatal(err)
		}
		if canonical != projected {
			t.Fatalf("knowledge message projection for %q = %t, want %t", value, projected, canonical)
		}
	}
	if err := checkKnowledgeMessageProjection(database, func(rows *sql.Rows) error { return rows.Err() }); err != nil {
		t.Fatal(err)
	}
	logEntries, err := os.ReadDir(filepath.Join(dataRoot, "logs"))
	if err != nil {
		t.Fatal(err)
	}
	if len(logEntries) != 0 {
		t.Fatalf("unexpected inbox log artifacts: %v", logEntries)
	}
}

func TestKnowledgeMessageProjectionOracleRejectsMutations(t *testing.T) {
	for _, mutation := range []struct {
		name    string
		apply   func(*sql.DB) error
		rowsErr func(*sql.Rows) error
	}{
		{
			name: "revision",
			apply: func(database *sql.DB) error {
				_, err := database.Exec(`UPDATE knowledge_fts SET revision = zeroblob(32) WHERE source_kind = 'message'`)
				return err
			},
			rowsErr: func(rows *sql.Rows) error { return rows.Err() },
		},
		{
			name: "source version",
			apply: func(database *sql.DB) error {
				_, err := database.Exec(`UPDATE knowledge_fts SET source_version = 1 WHERE source_kind = 'message'`)
				return err
			},
			rowsErr: func(rows *sql.Rows) error { return rows.Err() },
		},
		{
			name:    "iterator error",
			apply:   func(*sql.DB) error { return nil },
			rowsErr: func(*sql.Rows) error { return errors.New("injected iterator error") },
		},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			database := openKnowledgeOracleDatabase(t)
			defer database.Close()
			if err := mutation.apply(database); err != nil {
				t.Fatal(err)
			}
			if err := checkKnowledgeMessageProjection(database, mutation.rowsErr); err == nil {
				t.Fatal("knowledge projection oracle accepted a mutation")
			}
		})
	}
}

func openKnowledgeOracleDatabase(t *testing.T) *sql.DB {
	t.Helper()
	dataRoot := t.TempDir()
	app, err := New(context.Background(), Config{DataRoot: dataRoot})
	if err != nil {
		t.Fatal(err)
	}
	credential, err := authority.ReadCredentialFile(filepath.Join(dataRoot, "owner.key"))
	if err != nil {
		app.Close()
		t.Fatal(err)
	}
	bootstrap, err := app.store.EnsureAuthority(context.Background(), credential, time.Now())
	if err != nil {
		app.Close()
		t.Fatal(err)
	}
	owner := store.Principal{Kind: "human", ID: bootstrap.Human.ID, OrganizationID: bootstrap.Organization.ID}
	space, err := app.store.CreateGroup(context.Background(), store.CreateGroupParams{RequestID: uuid.NewString(), Actor: owner, Name: "knowledge oracle", Now: time.Now()})
	if err != nil {
		app.Close()
		t.Fatal(err)
	}
	if _, err := app.store.SendMessage(context.Background(), store.SendMessageParams{
		RequestID: uuid.NewString(), Actor: owner, Target: store.MessageTarget{Kind: store.MessageTargetSpace, ID: space.ID}, Body: "knowledge oracle source", Now: time.Now(),
	}); err != nil {
		app.Close()
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", filepath.Join(dataRoot, "data", "server.db"))
	if err != nil {
		app.Close()
		t.Fatal(err)
	}
	waitForKnowledgeMessages(t, database)
	if err := app.Close(); err != nil {
		database.Close()
		t.Fatal(err)
	}
	return database
}

func checkKnowledgeMessageProjection(database *sql.DB, rowsErr func(*sql.Rows) error) error {
	rows, err := database.Query(`SELECT f.source_id, f.source_version, f.revision, f.body, m.target_sequence FROM knowledge_fts f LEFT JOIN messages m ON m.id = f.source_id WHERE f.source_kind = 'message'`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, body string
		var sourceVersion uint64
		var revision []byte
		var sequence sql.NullInt64
		if err := rows.Scan(&id, &sourceVersion, &revision, &body, &sequence); err != nil {
			return err
		}
		if !sequence.Valid || sequence.Int64 < 0 || sourceVersion != 0 || body == "" {
			return fmt.Errorf("invalid knowledge message projection %q", id)
		}
		wantRevision := store.KnowledgeMessageRevision(id, uint64(sequence.Int64))
		if string(revision) != string(wantRevision[:]) {
			return fmt.Errorf("knowledge message projection %q revision is not canonical", id)
		}
		var canonicalBody string
		if err := database.QueryRow(`SELECT body FROM messages WHERE id = ?`, id).Scan(&canonicalBody); err != nil {
			return fmt.Errorf("read canonical knowledge message %q: %w", id, err)
		}
		if canonicalBody != body {
			return fmt.Errorf("knowledge message projection %q body is not canonical", id)
		}
	}
	return rowsErr(rows)
}

func waitForKnowledgeMessages(t *testing.T, database *sql.DB) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var applied, messageCount, projectedCount uint64
		var status string
		err := database.QueryRow(`SELECT applied_sequence, status FROM knowledge_index_state WHERE singleton = 1`).Scan(&applied, &status)
		if err == nil && status == store.KnowledgeIndexReady {
			err = database.QueryRow(`SELECT count(*) FROM messages`).Scan(&messageCount)
			if err == nil {
				err = database.QueryRow(`SELECT count(*) FROM knowledge_fts WHERE source_kind = 'message'`).Scan(&projectedCount)
			}
			if err == nil && projectedCount == messageCount {
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("knowledge runner did not project all canonical messages")
}

func quoteSQLiteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}
