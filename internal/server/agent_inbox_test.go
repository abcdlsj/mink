package server

import (
	"context"
	"database/sql"
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

func TestAgentInboxHTTPAllProceduresRequireCurrentRuntime(t *testing.T) {
	dataRoot := t.TempDir()
	api := openFactsAPI(t, dataRoot)
	defer api.close(t)
	computer, agent, placement, registrationKey := createActiveRuntimeBinding(t, api)
	runtimeClient := runtimev1connect.NewAgentRuntimeServiceClient(api.http.Client(), api.http.URL)
	oldSession := createRuntimeOverHTTP(t, runtimeClient, computer.GetId(), registrationKey, agent.GetId(), placement.GetGeneration())
	currentSession := createRuntimeOverHTTP(t, runtimeClient, computer.GetId(), registrationKey, agent.GetId(), placement.GetGeneration())
	ownerCredential, err := authority.ReadCredentialFile(filepath.Join(dataRoot, "owner.key"))
	if err != nil {
		t.Fatal(err)
	}
	client := inboxv1connect.NewInboxServiceClient(api.http.Client(), api.http.URL)
	itemID, draftID, spaceID, threadID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	requestID := uuid.NewString()
	calls := map[string]func(string) error{
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
	for name, call := range calls {
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

func TestAgentInboxBrowserSessionCannotAuthenticateRuntime(t *testing.T) {
	dataRoot := t.TempDir()
	api := openBrowserServer(t, dataRoot)
	defer api.close(t)
	credential, err := authority.ReadCredentialFile(filepath.Join(dataRoot, "owner.key"))
	if err != nil {
		t.Fatal(err)
	}
	client := inboxv1connect.NewInboxServiceClient(browserClient(t, api.origin, credential), api.origin)
	_, err = client.GetInboxNotice(context.Background(), connect.NewRequest(&inboxv1.GetInboxNoticeRequest{}))
	assertConnectCode(t, err, connect.CodeUnauthenticated)
}

func TestAgentInboxHTTPFreshHeldResolveAndRestartReplay(t *testing.T) {
	dataRoot := t.TempDir()
	api := openFactsAPI(t, dataRoot)
	computer, agent, placement, registrationKey := createActiveRuntimeBinding(t, api)
	runtimeClient := runtimev1connect.NewAgentRuntimeServiceClient(api.http.Client(), api.http.URL)
	session := createRuntimeOverHTTP(t, runtimeClient, computer.GetId(), registrationKey, agent.GetId(), placement.GetGeneration())
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
		RequestId: uuid.NewString(), Target: spaceTarget(spaceID), Body: body, MentionedAgentIds: []string{agentID},
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
	rows, err = database.Query(`SELECT f.source_id, f.source_version, f.revision, f.body, m.target_sequence FROM knowledge_fts f LEFT JOIN messages m ON m.id = f.source_id WHERE f.source_kind = 'message'`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id, body string
		var sourceVersion uint64
		var revision []byte
		var sequence sql.NullInt64
		if err := rows.Scan(&id, &sourceVersion, &revision, &body, &sequence); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if !sequence.Valid || sequence.Int64 < 0 || sourceVersion != 0 || body == "" {
			rows.Close()
			t.Fatalf("invalid knowledge message projection %q", id)
		}
		wantRevision := store.KnowledgeMessageRevision(id, uint64(sequence.Int64))
		if string(revision) != string(wantRevision[:]) {
			rows.Close()
			t.Fatalf("knowledge message projection %q revision is not canonical", id)
		}
		var canonicalBody string
		if err := database.QueryRow(`SELECT body FROM messages WHERE id = ?`, id).Scan(&canonicalBody); err != nil || canonicalBody != body {
			rows.Close()
			t.Fatalf("knowledge message projection %q is not canonical: %q, %v", id, body, err)
		}
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Err(); err != nil {
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

func waitForKnowledgeMessages(t *testing.T, database *sql.DB) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var activeGeneration, messageCount, projectedCount uint64
		var status string
		err := database.QueryRow(`SELECT active_generation, status FROM knowledge_index_metadata WHERE singleton = 1`).Scan(&activeGeneration, &status)
		if err == nil && activeGeneration != 0 && status == store.KnowledgeIndexReady {
			err = database.QueryRow(`SELECT count(*) FROM messages`).Scan(&messageCount)
			if err == nil {
				err = database.QueryRow(`SELECT count(*) FROM knowledge_fts WHERE generation = ? AND source_kind = 'message'`, activeGeneration).Scan(&projectedCount)
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
