package server

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	inboxv1 "github.com/abcdlsj/sumi/gen/go/sumi/inbox/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/inbox/v1/inboxv1connect"
	grantv1 "github.com/abcdlsj/sumi/gen/go/sumi/grant/v1"
	runv1 "github.com/abcdlsj/sumi/gen/go/sumi/run/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/run/v1/runv1connect"
	runtimev1 "github.com/abcdlsj/sumi/gen/go/sumi/runtime/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/runtime/v1/runtimev1connect"
	spacev1 "github.com/abcdlsj/sumi/gen/go/sumi/space/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/space/v1/spacev1connect"
	"github.com/abcdlsj/sumi/internal/authority"
	"github.com/abcdlsj/sumi/internal/store"
	"github.com/google/uuid"
)

func TestMessageHTTPHumanAndAgentShareCanonicalSurfaceWithRuntimeFailClosed(t *testing.T) {
	dataRoot := t.TempDir()
	api := openFactsAPI(t, dataRoot)
	defer api.close(t)
	computer, agent, placement, registrationKey := createActiveRuntimeBinding(t, api)
	runtimeClient := runtimev1connect.NewRuntimeServiceClient(api.http.Client(), api.http.URL)
	stale := createRuntimeOverHTTP(t, runtimeClient, computer.GetId(), registrationKey, agent.GetId(), placement.GetDesiredRevision())
	current := createRuntimeOverHTTP(t, runtimeClient, computer.GetId(), registrationKey, agent.GetId(), placement.GetDesiredRevision())
	group := createInboxSpace(t, api, dataRoot, agent.GetId())
	collaboration := spacev1connect.NewCollaborationServiceClient(api.http.Client(), api.http.URL)
	human := spacev1connect.NewCollaborationServiceClient(api.http.Client(), api.http.URL, browserSessionAuth("abcdefghijklmnopqrstuvwxyz-ABCDEFGHIJKLMNOP", ""))
	ownerCredential, err := authority.ReadCredentialFile(filepath.Join(dataRoot, "owner.key"))
	if err != nil {
		t.Fatal(err)
	}
	bootstrap, err := api.app.store.EnsureAuthority(context.Background(), ownerCredential, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	runtimePrincipal := store.Principal{Kind: store.PrincipalAgent, ID: agent.GetId(), OrganizationID: bootstrap.Organization.ID}
	if _, err := api.app.store.IssueGrant(context.Background(), store.IssueGrantParams{
		RequestID: uuid.NewString(), Actor: store.Principal{Kind: store.PrincipalHuman, ID: bootstrap.Human.ID, OrganizationID: bootstrap.Organization.ID},
		Subject: runtimePrincipal, Capability: store.CapabilityRunExecute,
		Scope: store.Scope{Kind: "agent", ID: agent.GetId()}, ParentGrantID: bootstrap.RootGrant.ID, Now: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	humanResponse, err := human.SendMessage(context.Background(), connect.NewRequest(&spacev1.SendMessageRequest{
		RequestId: uuid.NewString(), Target: spaceTarget(group.GetId()), Body: "human canonical message",
		MentionedPrincipals: []*spacev1.Principal{{Kind: spacev1.PrincipalKind_PRINCIPAL_KIND_AGENT, Id: agent.GetId()}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	humanMessage := humanResponse.Msg.GetMessage()
	if humanMessage.GetAuthor().GetKind() != spacev1.PrincipalKind_PRINCIPAL_KIND_HUMAN || humanMessage.GetAuthor().GetId() != bootstrap.Human.ID {
		t.Fatalf("Human message author = %+v", humanMessage.GetAuthor())
	}

	staleSend := runtimeRequest(&spacev1.SendMessageRequest{
		RequestId: uuid.NewString(), Target: spaceTarget(group.GetId()), Body: "stale runtime must fail",
	}, stale.GetToken())
	_, err = collaboration.SendMessage(context.Background(), staleSend)
	assertConnectCode(t, err, connect.CodeUnauthenticated)
	inbox := inboxv1connect.NewInboxServiceClient(api.http.Client(), api.http.URL)
	item := findInboxItem(t, inbox, current.GetToken(), humanMessage.GetId())
	observeInbox(t, inbox, current.GetToken(), item.GetTarget())
	runs := runv1connect.NewRunServiceClient(api.http.Client(), api.http.URL)
	listedRuns, err := runs.ListRuns(context.Background(), runtimeRequest(&runv1.ListRunsRequest{Limit: 10}, current.GetToken()))
	if err != nil || len(listedRuns.Msg.GetRuns()) != 1 {
		t.Fatalf("agent Runs = %+v, %v", listedRuns, err)
	}
	claimed, err := runs.ClaimRun(context.Background(), runtimeRequest(&runv1.ClaimRunRequest{
		RequestId: uuid.NewString(), RunId: listedRuns.Msg.GetRuns()[0].GetId(),
	}, current.GetToken()))
	if err != nil {
		t.Fatal(err)
	}
	run := claimed.Msg.GetRun()

	agentResponse, err := collaboration.SendMessage(context.Background(), runtimeRequest(&spacev1.SendMessageRequest{
		RequestId: uuid.NewString(), Target: spaceTarget(group.GetId()), Body: "agent canonical message",
		MentionedPrincipals: []*spacev1.Principal{{Kind: spacev1.PrincipalKind_PRINCIPAL_KIND_HUMAN, Id: bootstrap.Human.ID}},
		RunProof:            &grantv1.RunProof{RunId: run.GetId(), Attempt: run.GetAttempt(), Fence: run.GetFence()},
	}, current.GetToken()))
	if err != nil {
		t.Fatal(err)
	}
	agentMessage := agentResponse.Msg.GetMessage()
	if agentMessage.GetAuthor().GetKind() != spacev1.PrincipalKind_PRINCIPAL_KIND_AGENT || agentMessage.GetAuthor().GetId() != agent.GetId() {
		t.Fatalf("Agent message author = %+v", agentMessage.GetAuthor())
	}
	ownerInbox := inboxv1connect.NewInboxServiceClient(api.http.Client(), api.http.URL, browserSessionAuth("abcdefghijklmnopqrstuvwxyz-ABCDEFGHIJKLMNOP", ""))
	humanItems, err := ownerInbox.ListInboxItems(context.Background(), connect.NewRequest(&inboxv1.ListInboxItemsRequest{Limit: 10}))
	if err != nil || len(humanItems.Msg.GetItems()) != 1 || humanItems.Msg.GetItems()[0].GetTriggerMessageId() != agentMessage.GetId() || humanItems.Msg.GetItems()[0].GetRecipient().GetKind() != spacev1.PrincipalKind_PRINCIPAL_KIND_HUMAN {
		t.Fatalf("Agent-to-Human mention Inbox = %+v, %v", humanItems, err)
	}
	if _, err := human.SendMessage(context.Background(), connect.NewRequest(&spacev1.SendMessageRequest{
		RequestId: uuid.NewString(),
		Target:    &spacev1.MessageTarget{Target: &spacev1.MessageTarget_ThreadRootMessageId{ThreadRootMessageId: humanMessage.GetId()}},
		Body:      "human thread reply",
	})); err != nil {
		t.Fatal(err)
	}
	got, err := collaboration.GetMessage(context.Background(), runtimeRequest(&spacev1.GetMessageRequest{MessageId: agentMessage.GetId()}, current.GetToken()))
	if err != nil || got.Msg.GetMessage().GetId() != agentMessage.GetId() || got.Msg.GetMessage().GetAuthor().GetId() != agent.GetId() {
		t.Fatalf("Agent runtime GetMessage = %+v, %v", got, err)
	}
	thread, err := collaboration.GetThread(context.Background(), runtimeRequest(&spacev1.GetThreadRequest{ThreadRootMessageId: humanMessage.GetId()}, current.GetToken()))
	if err != nil || thread.Msg.GetThread().GetId() != humanMessage.GetId() || thread.Msg.GetThread().GetSpaceId() != group.GetId() {
		t.Fatalf("Agent runtime GetThread = %+v, %v", thread, err)
	}
	listed, err := collaboration.ListMessages(context.Background(), runtimeRequest(&spacev1.ListMessagesRequest{
		Target: spaceTarget(group.GetId()), Limit: 10,
	}, current.GetToken()))
	if err != nil || len(listed.Msg.GetMessages()) != 2 {
		t.Fatalf("Agent runtime ListMessages = %+v, %v", listed, err)
	}

	replacement := createRuntimeOverHTTP(t, runtimeClient, computer.GetId(), registrationKey, agent.GetId(), placement.GetDesiredRevision())
	_, err = collaboration.GetMessage(context.Background(), runtimeRequest(&spacev1.GetMessageRequest{MessageId: agentMessage.GetId()}, current.GetToken()))
	assertConnectCode(t, err, connect.CodeUnauthenticated)
	if _, err := collaboration.GetMessage(context.Background(), runtimeRequest(&spacev1.GetMessageRequest{MessageId: agentMessage.GetId()}, replacement.GetToken())); err != nil {
		t.Fatalf("replacement runtime read: %v", err)
	}
	revoke := runtimeRequest(&runtimev1.RevokeSessionRequest{
		ComputerId: computer.GetId(), RegistrationKey: registrationKey,
	}, replacement.GetToken())
	if _, err := runtimeClient.RevokeSession(context.Background(), revoke); err != nil {
		t.Fatal(err)
	}
	_, err = collaboration.ListMessages(context.Background(), runtimeRequest(&spacev1.ListMessagesRequest{
		Target: spaceTarget(group.GetId()), Limit: 10,
	}, replacement.GetToken()))
	assertConnectCode(t, err, connect.CodeUnauthenticated)
	postRevoke := createRuntimeOverHTTP(t, runtimeClient, computer.GetId(), registrationKey, agent.GetId(), placement.GetDesiredRevision())
	if _, err := collaboration.ListMessages(context.Background(), runtimeRequest(&spacev1.ListMessagesRequest{
		Target: spaceTarget(group.GetId()), Limit: 10,
	}, postRevoke.GetToken())); err != nil {
		t.Fatalf("post-revoke runtime read: %v", err)
	}

	humanRead, err := human.GetMessage(context.Background(), connect.NewRequest(&spacev1.GetMessageRequest{MessageId: agentMessage.GetId()}))
	if err != nil || humanRead.Msg.GetMessage().GetAuthor().GetKind() != spacev1.PrincipalKind_PRINCIPAL_KIND_AGENT {
		t.Fatalf("Human canonical read regressed = %+v, %v", humanRead, err)
	}
}
