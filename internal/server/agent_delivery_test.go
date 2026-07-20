package server

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	agentv1 "github.com/abcdlsj/sumi/gen/go/sumi/agent/v1"
	computerv1 "github.com/abcdlsj/sumi/gen/go/sumi/computer/v1"
	deliveryv1 "github.com/abcdlsj/sumi/gen/go/sumi/delivery/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/delivery/v1/deliveryv1connect"
	grantv1 "github.com/abcdlsj/sumi/gen/go/sumi/grant/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/grant/v1/grantv1connect"
	"github.com/abcdlsj/sumi/gen/go/sumi/inbox/v1/inboxv1connect"
	placementv1 "github.com/abcdlsj/sumi/gen/go/sumi/placement/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/runtime/v1/runtimev1connect"
	spacev1 "github.com/abcdlsj/sumi/gen/go/sumi/space/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/space/v1/spacev1connect"
	"github.com/abcdlsj/sumi/internal/authority"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
)

func TestAgentDeliveryHTTPAllProceduresRequireCurrentRuntime(t *testing.T) {
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
	client := deliveryv1connect.NewDeliveryServiceClient(api.http.Client(), api.http.URL)
	requestID, deliveryID, runID, launchID, eventID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	calls := map[string]func(string) error{
		"list": func(token string) error {
			_, err := client.ListDeliveries(context.Background(), deliveryRequest(token, &deliveryv1.ListDeliveriesRequest{Limit: 1}))
			return err
		},
		"accept": func(token string) error {
			_, err := client.AcceptDelivery(context.Background(), deliveryRequest(token, &deliveryv1.AcceptDeliveryRequest{RequestId: requestID, DeliveryId: deliveryID}))
			return err
		},
		"get run": func(token string) error {
			_, err := client.GetRun(context.Background(), deliveryRequest(token, &deliveryv1.GetRunRequest{RunId: runID}))
			return err
		},
		"claim": func(token string) error {
			_, err := client.ClaimRun(context.Background(), deliveryRequest(token, &deliveryv1.ClaimRunRequest{RequestId: requestID, RunId: runID}))
			return err
		},
		"renew": func(token string) error {
			_, err := client.RenewRun(context.Background(), deliveryRequest(token, &deliveryv1.RenewRunRequest{RequestId: requestID, RunId: runID, LaunchId: launchID, Fence: 1}))
			return err
		},
		"complete": func(token string) error {
			_, err := client.CompleteRun(context.Background(), deliveryRequest(token, &deliveryv1.CompleteRunRequest{
				RequestId: requestID, OutboxEventId: eventID, RunId: runID, LaunchId: launchID,
				Fence: 1, Outcome: deliveryv1.RunOutcome_RUN_OUTCOME_SUCCEEDED, Body: "result",
			}))
			return err
		},
	}
	for name, call := range calls {
		for credentialName, token := range map[string]string{
			"missing": "", "human": ownerCredential, "wrong": "not-a-runtime-token", "replaced runtime": oldSession.GetToken(),
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

func TestAgentDeliveryBrowserSessionCannotAuthenticateRuntime(t *testing.T) {
	dataRoot := t.TempDir()
	api := openBrowserServer(t, dataRoot)
	defer api.close(t)
	credential, err := authority.ReadCredentialFile(filepath.Join(dataRoot, "owner.key"))
	if err != nil {
		t.Fatal(err)
	}
	client := deliveryv1connect.NewDeliveryServiceClient(browserClient(t, api.origin, credential), api.origin)
	_, err = client.ListDeliveries(context.Background(), connect.NewRequest(&deliveryv1.ListDeliveriesRequest{Limit: 1}))
	assertConnectCode(t, err, connect.CodeUnauthenticated)
}

func TestAgentDeliveryHTTPGrantRestartReclaimReplayAuthorizationAndQuiet(t *testing.T) {
	dataRoot := t.TempDir()
	api := openFactsAPI(t, dataRoot)
	computer, agent, placement, registrationKey := createActiveRuntimeBinding(t, api)
	runtimeClient := runtimev1connect.NewAgentRuntimeServiceClient(api.http.Client(), api.http.URL)
	oldSession := createRuntimeOverHTTP(t, runtimeClient, computer.GetId(), registrationKey, agent.GetId(), placement.GetGeneration())
	peerResponse, err := api.agents.CreateAgent(context.Background(), connect.NewRequest(agentRequest("delivery-peer")))
	if err != nil {
		api.close(t)
		t.Fatal(err)
	}
	peer := peerResponse.Msg.GetAgent()
	ownerOption := ownerClientAuthorization(t, dataRoot)
	grantClient := grantv1connect.NewGrantServiceClient(api.http.Client(), api.http.URL, ownerOption)
	collaborationClient := spacev1connect.NewCollaborationServiceClient(api.http.Client(), api.http.URL, ownerOption)
	rootGrantID := rootGrantOverHTTP(t, grantClient)
	group, grants := createDeliverySpaceAndGrantsHTTP(t, collaborationClient, grantClient, rootGrantID, agent.GetId(), peer.GetId())
	assertRunExecutePermissionHTTP(t, grantClient, agent.GetId(), true)
	inboxClient := inboxv1connect.NewInboxServiceClient(api.http.Client(), api.http.URL)
	deliveryClient := deliveryv1connect.NewDeliveryServiceClient(api.http.Client(), api.http.URL)
	trigger := sendMention(t, collaborationClient, group.GetId(), agent.GetId(), "delivery-http-trigger")
	item := findInboxItem(t, inboxClient, oldSession.GetToken(), trigger.GetId())
	observed := observeInbox(t, inboxClient, oldSession.GetToken(), item.GetTarget())
	listed, err := deliveryClient.ListDeliveries(context.Background(), deliveryRequest(oldSession.GetToken(), &deliveryv1.ListDeliveriesRequest{Limit: 200}))
	if err != nil || len(listed.Msg.GetDeliveries()) != 1 || listed.Msg.GetDeliveries()[0].GetTriggerMessageId() != trigger.GetId() {
		api.close(t)
		t.Fatalf("initial delivery list = %+v, %v", listed, err)
	}
	acceptRequest := &deliveryv1.AcceptDeliveryRequest{RequestId: uuid.NewString(), DeliveryId: listed.Msg.GetDeliveries()[0].GetId()}
	accepted, err := deliveryClient.AcceptDelivery(context.Background(), deliveryRequest(oldSession.GetToken(), acceptRequest))
	if err != nil || accepted.Msg.GetRun().GetBasisTargetSequence() != observed.GetHeadSequence() {
		api.close(t)
		t.Fatalf("accepted run = %+v, %v", accepted, err)
	}
	claimRequest := &deliveryv1.ClaimRunRequest{RequestId: uuid.NewString(), RunId: accepted.Msg.GetRun().GetId()}
	claimed, err := deliveryClient.ClaimRun(context.Background(), deliveryRequest(oldSession.GetToken(), claimRequest))
	if err != nil {
		api.close(t)
		t.Fatal(err)
	}
	renewRequest := &deliveryv1.RenewRunRequest{
		RequestId: uuid.NewString(), RunId: accepted.Msg.GetRun().GetId(),
		LaunchId: claimed.Msg.GetLaunch().GetId(), Fence: claimed.Msg.GetLaunch().GetFence(),
	}
	renewed, err := deliveryClient.RenewRun(context.Background(), deliveryRequest(oldSession.GetToken(), renewRequest))
	if err != nil || !renewed.Msg.GetLaunch().GetExpiresAt().AsTime().After(claimed.Msg.GetLaunch().GetExpiresAt().AsTime()) {
		api.close(t)
		t.Fatalf("renewed launch = %+v, %v", renewed, err)
	}
	api.close(t)
	api = openFactsAPI(t, dataRoot)
	deliveryClient = deliveryv1connect.NewDeliveryServiceClient(api.http.Client(), api.http.URL)
	restarted, err := deliveryClient.ListDeliveries(context.Background(), deliveryRequest(oldSession.GetToken(), &deliveryv1.ListDeliveriesRequest{Limit: 200}))
	if err != nil || restarted.Msg.GetActiveRun().GetId() != accepted.Msg.GetRun().GetId() ||
		restarted.Msg.GetActiveLaunch().GetId() != claimed.Msg.GetLaunch().GetId() {
		api.close(t)
		t.Fatalf("restart active discovery = %+v, %v", restarted, err)
	}
	api.close(t)
	expireLaunchFixture(t, dataRoot, claimed.Msg.GetLaunch().GetId())
	api = openFactsAPI(t, dataRoot)
	ownerOption = ownerClientAuthorization(t, dataRoot)
	computerResponse, err := api.computers.RegisterComputer(context.Background(), connect.NewRequest(&computerv1.RegisterComputerRequest{
		RegistrationKey: "delivery-http-migrated-registration-key", Name: "Delivery migrated host",
		Os: computerv1.OperatingSystem_OPERATING_SYSTEM_LINUX, Arch: computerv1.Architecture_ARCHITECTURE_ARM64,
	}))
	if err != nil {
		api.close(t)
		t.Fatal(err)
	}
	newComputer := computerResponse.Msg.GetComputer()
	placementResponse, err := api.placements.SetAgentPlacement(context.Background(), connect.NewRequest(&placementv1.SetAgentPlacementRequest{
		RequestId: uuid.NewString(), AgentId: agent.GetId(), ComputerId: newComputer.GetId(),
	}))
	if err != nil {
		api.close(t)
		t.Fatal(err)
	}
	newPlacement, err := api.placements.AcknowledgeAgentPlacement(context.Background(), connect.NewRequest(&placementv1.AcknowledgeAgentPlacementRequest{
		ComputerId: newComputer.GetId(), RegistrationKey: "delivery-http-migrated-registration-key",
		AgentId: agent.GetId(), Generation: placementResponse.Msg.GetPlacement().GetGeneration(),
		Result: placementv1.AcknowledgementResult_ACKNOWLEDGEMENT_RESULT_ACTIVE,
	}))
	if err != nil {
		api.close(t)
		t.Fatal(err)
	}
	runtimeClient = runtimev1connect.NewAgentRuntimeServiceClient(api.http.Client(), api.http.URL)
	newSession := createRuntimeOverHTTP(t, runtimeClient, newComputer.GetId(), "delivery-http-migrated-registration-key", agent.GetId(), newPlacement.Msg.GetPlacement().GetGeneration())
	deliveryClient = deliveryv1connect.NewDeliveryServiceClient(api.http.Client(), api.http.URL)
	if _, err := deliveryClient.ListDeliveries(context.Background(), deliveryRequest(oldSession.GetToken(), &deliveryv1.ListDeliveriesRequest{Limit: 200})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		api.close(t)
		t.Fatalf("old placement token error = %v", err)
	}
	migratedList, err := deliveryClient.ListDeliveries(context.Background(), deliveryRequest(newSession.GetToken(), &deliveryv1.ListDeliveriesRequest{Limit: 200}))
	if err != nil || migratedList.Msg.GetActiveRun().GetId() != accepted.Msg.GetRun().GetId() ||
		migratedList.Msg.GetActiveLaunch().GetId() != claimed.Msg.GetLaunch().GetId() ||
		migratedList.Msg.GetActiveLaunch().GetExpiresAt().AsTime().After(time.Now()) {
		api.close(t)
		t.Fatalf("migrated expired discovery = %+v, %v", migratedList, err)
	}
	reclaimRequest := &deliveryv1.ClaimRunRequest{RequestId: uuid.NewString(), RunId: accepted.Msg.GetRun().GetId()}
	reclaimed, err := deliveryClient.ClaimRun(context.Background(), deliveryRequest(newSession.GetToken(), reclaimRequest))
	if err != nil || reclaimed.Msg.GetLaunch().GetFence() != claimed.Msg.GetLaunch().GetFence()+1 ||
		reclaimed.Msg.GetLaunch().GetHolderComputerId() != newComputer.GetId() {
		api.close(t)
		t.Fatalf("reclaimed launch = %+v, %v", reclaimed, err)
	}
	staleRequest := &deliveryv1.CompleteRunRequest{
		RequestId: uuid.NewString(), OutboxEventId: uuid.NewString(), RunId: accepted.Msg.GetRun().GetId(),
		LaunchId: claimed.Msg.GetLaunch().GetId(), Fence: claimed.Msg.GetLaunch().GetFence(),
		Outcome: deliveryv1.RunOutcome_RUN_OUTCOME_SUCCEEDED, Body: "stale-holder-body-secret",
	}
	if _, err := deliveryClient.CompleteRun(context.Background(), deliveryRequest(newSession.GetToken(), staleRequest)); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		api.close(t)
		t.Fatalf("stale holder completion error = %v", err)
	}
	completeBody := "delivery-http-completion-body-secret"
	completeRequest := &deliveryv1.CompleteRunRequest{
		RequestId: uuid.NewString(), OutboxEventId: uuid.NewString(), RunId: accepted.Msg.GetRun().GetId(),
		LaunchId: reclaimed.Msg.GetLaunch().GetId(), Fence: reclaimed.Msg.GetLaunch().GetFence(),
		Outcome: deliveryv1.RunOutcome_RUN_OUTCOME_SUCCEEDED, Body: completeBody,
		MentionedAgentIds: []string{peer.GetId()},
	}
	completed, err := deliveryClient.CompleteRun(context.Background(), deliveryRequest(newSession.GetToken(), completeRequest))
	if err != nil || completed.Msg.GetMessage() == nil || completed.Msg.GetHeldDraft() != nil ||
		completed.Msg.GetRun().GetResultMessageId() != completed.Msg.GetMessage().GetId() {
		api.close(t)
		t.Fatalf("completed run = %+v, %v", completed, err)
	}
	replayed, err := deliveryClient.CompleteRun(context.Background(), deliveryRequest(newSession.GetToken(), completeRequest))
	if err != nil || !proto.Equal(completed.Msg, replayed.Msg) {
		api.close(t)
		t.Fatalf("completion replay = %+v, %v", replayed, err)
	}
	conflictBodies := []string{"event-conflict-body-secret", "request-conflict-body-secret", "run-conflict-body-secret"}
	conflicts := []*deliveryv1.CompleteRunRequest{
		{RequestId: uuid.NewString(), OutboxEventId: completeRequest.GetOutboxEventId()},
		{RequestId: completeRequest.GetRequestId(), OutboxEventId: uuid.NewString()},
		{RequestId: uuid.NewString(), OutboxEventId: uuid.NewString()},
	}
	for index, conflict := range conflicts {
		conflict.RunId = completeRequest.GetRunId()
		conflict.LaunchId = completeRequest.GetLaunchId()
		conflict.Fence = completeRequest.GetFence()
		conflict.Outcome = completeRequest.GetOutcome()
		conflict.Body = conflictBodies[index]
		_, err := deliveryClient.CompleteRun(context.Background(), deliveryRequest(newSession.GetToken(), conflict))
		if connect.CodeOf(err) != connect.CodeAlreadyExists {
			api.close(t)
			t.Fatalf("completion conflict %d error = %v", index, err)
		}
		for _, privateValue := range append([]string{newSession.GetToken(), completeBody}, conflictBodies...) {
			if strings.Contains(err.Error(), privateValue) {
				api.close(t)
				t.Fatalf("completion conflict error leaked %q: %v", privateValue, err)
			}
		}
	}
	grantClient = grantv1connect.NewGrantServiceClient(api.http.Client(), api.http.URL, ownerOption)
	collaborationClient = spacev1connect.NewCollaborationServiceClient(api.http.Client(), api.http.URL, ownerOption)
	grants[grantv1.Capability_CAPABILITY_RUN_EXECUTE] = exerciseGrantReplayAuthorization(t, deliveryClient, grantClient, rootGrantID, agent.GetId(), group.GetId(), newSession.GetToken(), completeRequest, completed.Msg, grants[grantv1.Capability_CAPABILITY_RUN_EXECUTE])
	grants[grantv1.Capability_CAPABILITY_SPACE_READ] = exerciseGrantReplayAuthorization(t, deliveryClient, grantClient, rootGrantID, agent.GetId(), group.GetId(), newSession.GetToken(), completeRequest, completed.Msg, grants[grantv1.Capability_CAPABILITY_SPACE_READ])
	grants[grantv1.Capability_CAPABILITY_MESSAGE_SEND] = exerciseGrantReplayAuthorization(t, deliveryClient, grantClient, rootGrantID, agent.GetId(), group.GetId(), newSession.GetToken(), completeRequest, completed.Msg, grants[grantv1.Capability_CAPABILITY_MESSAGE_SEND])
	if _, err := collaborationClient.RemoveMember(context.Background(), connect.NewRequest(&spacev1.RemoveMemberRequest{
		RequestId: uuid.NewString(), SpaceId: group.GetId(),
		Member: &spacev1.Principal{Kind: spacev1.PrincipalKind_PRINCIPAL_KIND_AGENT, Id: agent.GetId()},
	})); err != nil {
		api.close(t)
		t.Fatal(err)
	}
	if _, err := deliveryClient.CompleteRun(context.Background(), deliveryRequest(newSession.GetToken(), completeRequest)); connect.CodeOf(err) != connect.CodePermissionDenied {
		api.close(t)
		t.Fatalf("completion replay after membership removal error = %v", err)
	}
	if _, err := collaborationClient.AddMember(context.Background(), connect.NewRequest(&spacev1.AddMemberRequest{
		RequestId: uuid.NewString(), SpaceId: group.GetId(),
		Member: &spacev1.Principal{Kind: spacev1.PrincipalKind_PRINCIPAL_KIND_AGENT, Id: agent.GetId()},
	})); err != nil {
		api.close(t)
		t.Fatal(err)
	}
	replayed, err = deliveryClient.CompleteRun(context.Background(), deliveryRequest(newSession.GetToken(), completeRequest))
	if err != nil || !proto.Equal(replayed.Msg, completed.Msg) {
		api.close(t)
		t.Fatalf("completion replay after membership restore = %+v, %v", replayed, err)
	}
	api.close(t)
	api = openFactsAPI(t, dataRoot)
	deliveryClient = deliveryv1connect.NewDeliveryServiceClient(api.http.Client(), api.http.URL)
	replayed, err = deliveryClient.CompleteRun(context.Background(), deliveryRequest(newSession.GetToken(), completeRequest))
	if err != nil || !proto.Equal(replayed.Msg, completed.Msg) {
		api.close(t)
		t.Fatalf("completion restart replay = %+v, %v", replayed, err)
	}
	api.close(t)
	assertAgentDeliveryDatabase(t, dataRoot, accepted.Msg.GetRun().GetId(), completeRequest, staleRequest, conflicts, completed.Msg.GetMessage().GetId())
	ownerCredential, err := authority.ReadCredentialFile(filepath.Join(dataRoot, "owner.key"))
	if err != nil {
		t.Fatal(err)
	}
	assertAgentInboxDataRootQuiet(t, dataRoot,
		append([]string{completeBody, staleRequest.GetBody()}, conflictBodies...),
		[]string{oldSession.GetToken(), newSession.GetToken(), registrationKey, "delivery-http-migrated-registration-key", ownerCredential, dataRoot},
	)
	assertDeliveryRegistryQuiet(t, dataRoot, append([]string{completeBody, staleRequest.GetBody(), peer.GetId(), oldSession.GetToken(), newSession.GetToken()}, conflictBodies...)...)
}

func createDeliverySpaceAndGrantsHTTP(t *testing.T, collaborationClient spacev1connect.CollaborationServiceClient, grantClient grantv1connect.GrantServiceClient, rootGrantID, agentID, peerID string) (*spacev1.Space, map[grantv1.Capability]*grantv1.Grant) {
	t.Helper()
	groupResponse, err := collaborationClient.CreateGroup(context.Background(), connect.NewRequest(&spacev1.CreateGroupRequest{RequestId: uuid.NewString(), Name: "Agent Delivery Server"}))
	if err != nil {
		t.Fatal(err)
	}
	group := groupResponse.Msg.GetSpace()
	for _, memberID := range []string{agentID, peerID} {
		if _, err := collaborationClient.AddMember(context.Background(), connect.NewRequest(&spacev1.AddMemberRequest{
			RequestId: uuid.NewString(), SpaceId: group.GetId(),
			Member: &spacev1.Principal{Kind: spacev1.PrincipalKind_PRINCIPAL_KIND_AGENT, Id: memberID},
		})); err != nil {
			t.Fatal(err)
		}
	}
	grants := map[grantv1.Capability]*grantv1.Grant{}
	for _, value := range []struct {
		capability grantv1.Capability
		scope      *grantv1.Scope
	}{
		{grantv1.Capability_CAPABILITY_SPACE_READ, &grantv1.Scope{Kind: grantv1.ScopeKind_SCOPE_KIND_SPACE, Id: group.GetId()}},
		{grantv1.Capability_CAPABILITY_MESSAGE_SEND, &grantv1.Scope{Kind: grantv1.ScopeKind_SCOPE_KIND_SPACE, Id: group.GetId()}},
		{grantv1.Capability_CAPABILITY_RUN_EXECUTE, &grantv1.Scope{Kind: grantv1.ScopeKind_SCOPE_KIND_AGENT, Id: agentID}},
	} {
		grants[value.capability] = issueDeliveryGrantHTTP(t, grantClient, rootGrantID, agentID, value.capability, value.scope)
	}
	return group, grants
}

func rootGrantOverHTTP(t *testing.T, client grantv1connect.GrantServiceClient) string {
	t.Helper()
	response, err := client.ListGrants(context.Background(), connect.NewRequest(&grantv1.ListGrantsRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, grant := range response.Msg.GetGrants() {
		if grant.GetCapability() == grantv1.Capability_CAPABILITY_ORGANIZATION_ADMIN && grant.GetParentGrantId() == "" {
			return grant.GetId()
		}
	}
	t.Fatal("root grant not found through GrantService")
	return ""
}

func issueDeliveryGrantHTTP(t *testing.T, client grantv1connect.GrantServiceClient, rootGrantID, agentID string, capability grantv1.Capability, scope *grantv1.Scope) *grantv1.Grant {
	t.Helper()
	response, err := client.IssueGrant(context.Background(), connect.NewRequest(&grantv1.IssueGrantRequest{
		RequestId: uuid.NewString(), Subject: &grantv1.Principal{Kind: grantv1.PrincipalKind_PRINCIPAL_KIND_AGENT, Id: agentID},
		Capability: capability, Scope: scope, ParentGrantId: rootGrantID,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if response.Msg.GetGrant().GetCapability() != capability || response.Msg.GetGrant().GetScope().GetKind() != scope.GetKind() || response.Msg.GetGrant().GetScope().GetId() != scope.GetId() {
		t.Fatalf("grant response = %+v", response.Msg.GetGrant())
	}
	return response.Msg.GetGrant()
}

func assertRunExecutePermissionHTTP(t *testing.T, client grantv1connect.GrantServiceClient, agentID string, want bool) {
	t.Helper()
	response, err := client.CheckPermission(context.Background(), connect.NewRequest(&grantv1.CheckPermissionRequest{
		Subject:    &grantv1.Principal{Kind: grantv1.PrincipalKind_PRINCIPAL_KIND_AGENT, Id: agentID},
		Capability: grantv1.Capability_CAPABILITY_RUN_EXECUTE,
		Scope:      &grantv1.Scope{Kind: grantv1.ScopeKind_SCOPE_KIND_AGENT, Id: agentID},
	}))
	if err != nil || response.Msg.GetAllowed() != want {
		t.Fatalf("run.execute permission = %+v, %v, want %t", response, err, want)
	}
}

func exerciseGrantReplayAuthorization(t *testing.T, deliveryClient deliveryv1connect.DeliveryServiceClient, grantClient grantv1connect.GrantServiceClient, rootGrantID, agentID, spaceID, token string, request *deliveryv1.CompleteRunRequest, want *deliveryv1.CompleteRunResponse, current *grantv1.Grant) *grantv1.Grant {
	t.Helper()
	if _, err := grantClient.RevokeGrant(context.Background(), connect.NewRequest(&grantv1.RevokeGrantRequest{RequestId: uuid.NewString(), GrantId: current.GetId()})); err != nil {
		t.Fatal(err)
	}
	if _, err := deliveryClient.CompleteRun(context.Background(), deliveryRequest(token, request)); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("completion replay after %s revoke error = %v", current.GetCapability(), err)
	}
	scope := &grantv1.Scope{Kind: grantv1.ScopeKind_SCOPE_KIND_SPACE, Id: spaceID}
	if current.GetCapability() == grantv1.Capability_CAPABILITY_RUN_EXECUTE {
		scope = &grantv1.Scope{Kind: grantv1.ScopeKind_SCOPE_KIND_AGENT, Id: agentID}
		assertRunExecutePermissionHTTP(t, grantClient, agentID, false)
	}
	replacement := issueDeliveryGrantHTTP(t, grantClient, rootGrantID, agentID, current.GetCapability(), scope)
	if current.GetCapability() == grantv1.Capability_CAPABILITY_RUN_EXECUTE {
		assertRunExecutePermissionHTTP(t, grantClient, agentID, true)
	}
	replayed, err := deliveryClient.CompleteRun(context.Background(), deliveryRequest(token, request))
	if err != nil || !proto.Equal(replayed.Msg, want) {
		t.Fatalf("completion replay after %s restore = %+v, %v", current.GetCapability(), replayed, err)
	}
	return replacement
}

func expireLaunchFixture(t *testing.T, dataRoot, launchID string) {
	t.Helper()
	database, err := sql.Open("sqlite", filepath.Join(dataRoot, "data", "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var claimedAt, expiresAt int64
	if err := tx.QueryRow(`SELECT claimed_at, expires_at FROM run_launches WHERE id = ? AND closed_at IS NULL`, launchID).Scan(&claimedAt, &expiresAt); err != nil {
		t.Fatal(err)
	}
	delta := time.Now().Add(-time.Minute).UnixNano() - expiresAt
	if _, err := tx.Exec(`UPDATE run_launches SET claimed_at = claimed_at + ?, expires_at = expires_at + ? WHERE id = ? AND closed_at IS NULL`, delta, delta, launchID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	assertSQLiteClean(t, database)
}

func assertAgentDeliveryDatabase(t *testing.T, dataRoot, runID string, completeRequest, staleRequest *deliveryv1.CompleteRunRequest, conflicts []*deliveryv1.CompleteRunRequest, messageID string) {
	t.Helper()
	database, err := sql.Open("sqlite", filepath.Join(dataRoot, "data", "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	assertSQLiteClean(t, database)
	var messages, drafts, receipts, requests int
	if err := database.QueryRow(`SELECT count(*) FROM messages WHERE request_id = ? AND id = ?`, completeRequest.GetRequestId(), messageID).Scan(&messages); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT count(*) FROM agent_held_drafts WHERE inbox_item_id = (SELECT inbox_item_id FROM deliveries WHERE id = (SELECT delivery_id FROM runs WHERE id = ?))`, runID).Scan(&drafts); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT count(*) FROM run_completion_receipts WHERE run_id = ? AND outbox_event_id = ? AND request_id = ?`, runID, completeRequest.GetOutboxEventId(), completeRequest.GetRequestId()).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT count(*) FROM agent_requests WHERE request_id = ?`, completeRequest.GetRequestId()).Scan(&requests); err != nil {
		t.Fatal(err)
	}
	if messages != 1 || drafts != 0 || receipts != 1 || requests != 1 {
		t.Fatalf("completion facts = messages:%d drafts:%d receipts:%d requests:%d", messages, drafts, receipts, requests)
	}
	for _, requestID := range append([]string{staleRequest.GetRequestId()}, conflictNewRequestIDs(conflicts, completeRequest.GetRequestId())...) {
		var count int
		if err := database.QueryRow(`SELECT count(*) FROM agent_requests WHERE request_id = ?`, requestID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("rejected request %s persisted %d receipts", requestID, count)
		}
	}
	var serverOutboxTables int
	if err := database.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE type = 'table' AND lower(name) LIKE '%outbox%'`).Scan(&serverOutboxTables); err != nil {
		t.Fatal(err)
	}
	if serverOutboxTables != 0 {
		t.Fatalf("unexpected Server outbox tables = %d", serverOutboxTables)
	}
}

func conflictNewRequestIDs(values []*deliveryv1.CompleteRunRequest, committedRequestID string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value.GetRequestId() != committedRequestID {
			result = append(result, value.GetRequestId())
		}
	}
	return result
}

func assertDeliveryRegistryQuiet(t *testing.T, dataRoot string, forbidden ...string) {
	t.Helper()
	database, err := sql.Open("sqlite", filepath.Join(dataRoot, "data", "server.db"))
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

func assertSQLiteClean(t *testing.T, database *sql.DB) {
	t.Helper()
	rows, err := database.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	if rows.Next() {
		rows.Close()
		t.Fatal("foreign key check returned a violation")
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	var integrity string
	if err := database.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity check = %q, %v", integrity, err)
	}
}

func deliveryRequest[T any](token string, message *T) *connect.Request[T] {
	request := connect.NewRequest(message)
	if token != "" {
		request.Header().Set("Authorization", "Bearer "+token)
	}
	return request
}

func agentRequest(name string) *agentv1.CreateAgentRequest {
	return &agentv1.CreateAgentRequest{RequestId: uuid.NewString(), Name: name, Driver: agentv1.Driver_DRIVER_NATIVE}
}
