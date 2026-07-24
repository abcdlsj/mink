package server

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	agentv1 "github.com/abcdlsj/sumi/gen/go/sumi/agent/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/agent/v1/agentv1connect"
	computerv1 "github.com/abcdlsj/sumi/gen/go/sumi/computer/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/computer/v1/computerv1connect"
	grantv1 "github.com/abcdlsj/sumi/gen/go/sumi/grant/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/grant/v1/grantv1connect"
	"github.com/abcdlsj/sumi/gen/go/sumi/inbox/v1/inboxv1connect"
	runv1 "github.com/abcdlsj/sumi/gen/go/sumi/run/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/run/v1/runv1connect"
	"github.com/abcdlsj/sumi/gen/go/sumi/runtime/v1/runtimev1connect"
	spacev1 "github.com/abcdlsj/sumi/gen/go/sumi/space/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/space/v1/spacev1connect"
	"github.com/abcdlsj/sumi/internal/authority"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestRunHTTPAllProceduresRequireCurrentRuntime(t *testing.T) {
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
	client := runv1connect.NewRunServiceClient(api.http.Client(), api.http.URL)
	requestID, runID, eventID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	calls := map[string]func(string) error{
		"list": func(token string) error {
			_, err := client.ListRuns(context.Background(), runtimeRequestHTTP(token, &runv1.ListRunsRequest{Limit: 1}))
			return err
		},
		"get": func(token string) error {
			_, err := client.GetRun(context.Background(), runtimeRequestHTTP(token, &runv1.GetRunRequest{RunId: runID}))
			return err
		},
		"claim": func(token string) error {
			_, err := client.ClaimRun(context.Background(), runtimeRequestHTTP(token, &runv1.ClaimRunRequest{RequestId: requestID, RunId: runID}))
			return err
		},
		"renew": func(token string) error {
			_, err := client.RenewRun(context.Background(), runtimeRequestHTTP(token, &runv1.RenewRunRequest{RequestId: requestID, RunId: runID, Attempt: 1, Fence: 1}))
			return err
		},
		"cancel": func(token string) error {
			_, err := client.CancelRun(context.Background(), runtimeRequestHTTP(token, &runv1.CancelRunRequest{RequestId: requestID, RunId: runID, Attempt: 1, Fence: 1}))
			return err
		},
		"complete": func(token string) error {
			_, err := client.CompleteRun(context.Background(), runtimeRequestHTTP(token, &runv1.CompleteRunRequest{
				RequestId: requestID, OutboxEventId: eventID, RunId: runID, Attempt: 1, Fence: 1,
				Outcome: runv1.RunOutcome_RUN_OUTCOME_SUCCEEDED, Body: "result",
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

func TestRunHTTPQueueClaimCompleteAndReplay(t *testing.T) {
	dataRoot := t.TempDir()
	api := openFactsAPI(t, dataRoot)
	defer api.close(t)
	computer, agent, placement, registrationKey := createActiveRuntimeBinding(t, api)
	runtimeClient := runtimev1connect.NewAgentRuntimeServiceClient(api.http.Client(), api.http.URL)
	session := createRuntimeOverHTTP(t, runtimeClient, computer.GetId(), registrationKey, agent.GetId(), placement.GetDesiredRevision())
	ownerOption := ownerClientAuthorization(t, dataRoot)
	grants := grantv1connect.NewGrantServiceClient(api.http.Client(), api.http.URL, ownerOption)
	spaces := spacev1connect.NewCollaborationServiceClient(api.http.Client(), api.http.URL, ownerOption)
	rootGrantID := rootGrantOverHTTP(t, grants)
	groupResponse, err := spaces.CreateGroup(context.Background(), connect.NewRequest(&spacev1.CreateGroupRequest{
		RequestId: uuid.NewString(), Name: "Run HTTP",
	}))
	if err != nil {
		t.Fatal(err)
	}
	group := groupResponse.Msg.GetSpace()
	if _, err := spaces.AddMember(context.Background(), connect.NewRequest(&spacev1.AddMemberRequest{
		RequestId: uuid.NewString(), SpaceId: group.GetId(),
		Member: &spacev1.Principal{Kind: spacev1.PrincipalKind_PRINCIPAL_KIND_AGENT, Id: agent.GetId()},
	})); err != nil {
		t.Fatal(err)
	}
	for _, value := range []struct {
		capability grantv1.Capability
		scope      *grantv1.Scope
	}{
		{grantv1.Capability_CAPABILITY_SPACE_READ, &grantv1.Scope{Kind: grantv1.ScopeKind_SCOPE_KIND_SPACE, Id: group.GetId()}},
		{grantv1.Capability_CAPABILITY_MESSAGE_SEND, &grantv1.Scope{Kind: grantv1.ScopeKind_SCOPE_KIND_SPACE, Id: group.GetId()}},
		{grantv1.Capability_CAPABILITY_RUN_EXECUTE, &grantv1.Scope{Kind: grantv1.ScopeKind_SCOPE_KIND_AGENT, Id: agent.GetId()}},
	} {
		issueRunGrantHTTP(t, grants, rootGrantID, agent.GetId(), value.capability, value.scope)
	}
	trigger := sendMention(t, spaces, group.GetId(), agent.GetId(), "execute")
	inbox := inboxv1connect.NewInboxServiceClient(api.http.Client(), api.http.URL)
	item := findInboxItem(t, inbox, session.GetToken(), trigger.GetId())
	observed := observeInbox(t, inbox, session.GetToken(), item.GetTarget())
	runs := runv1connect.NewRunServiceClient(api.http.Client(), api.http.URL)
	listed, err := runs.ListRuns(context.Background(), runtimeRequestHTTP(session.GetToken(), &runv1.ListRunsRequest{Limit: 20}))
	if err != nil || len(listed.Msg.GetRuns()) != 1 || listed.Msg.GetRuns()[0].GetTriggerMessageId() != trigger.GetId() {
		t.Fatalf("queued runs = %+v, %v", listed, err)
	}
	claimRequest := &runv1.ClaimRunRequest{RequestId: uuid.NewString(), RunId: listed.Msg.GetRuns()[0].GetId()}
	claimed, err := runs.ClaimRun(context.Background(), runtimeRequestHTTP(session.GetToken(), claimRequest))
	if err != nil {
		t.Fatal(err)
	}
	run := claimed.Msg.GetRun()
	if run.GetState() != runv1.RunState_RUN_STATE_RUNNING || run.GetAttempt() != 1 || run.GetFence() == 0 ||
		run.GetInputBasisTargetSequence() != observed.GetHeadSequence() || run.GetLeaseHolderComputerId() != computer.GetId() {
		t.Fatalf("claimed run = %+v", run)
	}
	replayedClaim, err := runs.ClaimRun(context.Background(), runtimeRequestHTTP(session.GetToken(), claimRequest))
	if err != nil || replayedClaim.Msg.GetRun().GetFence() != run.GetFence() {
		t.Fatalf("claim replay = %+v, %v", replayedClaim, err)
	}
	completeRequest := &runv1.CompleteRunRequest{
		RequestId: uuid.NewString(), OutboxEventId: uuid.NewString(), RunId: run.GetId(),
		Attempt: run.GetAttempt(), Fence: run.GetFence(), Outcome: runv1.RunOutcome_RUN_OUTCOME_SUCCEEDED,
		Body: "completed", Usage: &runv1.RunUsage{InputUnits: 4, OutputUnits: 2},
	}
	completed, err := runs.CompleteRun(context.Background(), runtimeRequestHTTP(session.GetToken(), completeRequest))
	if err != nil || completed.Msg.GetRun().GetState() != runv1.RunState_RUN_STATE_SUCCEEDED || completed.Msg.GetMessage() == nil {
		t.Fatalf("completion = %+v, %v", completed, err)
	}
	replayed, err := runs.CompleteRun(context.Background(), runtimeRequestHTTP(session.GetToken(), completeRequest))
	if err != nil || replayed.Msg.GetMessage().GetId() != completed.Msg.GetMessage().GetId() {
		t.Fatalf("completion replay = %+v, %v", replayed, err)
	}
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
	t.Fatal("root grant not found")
	return ""
}

func issueRunGrantHTTP(t *testing.T, client grantv1connect.GrantServiceClient, rootGrantID, agentID string, capability grantv1.Capability, scope *grantv1.Scope) *grantv1.Grant {
	t.Helper()
	response, err := client.IssueGrant(context.Background(), connect.NewRequest(&grantv1.IssueGrantRequest{
		RequestId:  uuid.NewString(),
		Subject:    &grantv1.Principal{Kind: grantv1.PrincipalKind_PRINCIPAL_KIND_AGENT, Id: agentID},
		Capability: capability, Scope: scope, ParentGrantId: rootGrantID,
	}))
	if err != nil {
		t.Fatal(err)
	}
	return response.Msg.GetGrant()
}

func runtimeRequestHTTP[T any](token string, message *T) *connect.Request[T] {
	request := connect.NewRequest(message)
	if token != "" {
		request.Header().Set("Authorization", "Bearer "+token)
	}
	return request
}

func agentRequest(name string) *agentv1.CreateAgentRequest {
	return &agentv1.CreateAgentRequest{
		RequestId: uuid.NewString(), Handle: name, DisplayName: name,
		Role: "worker", Mission: "Exercise Server behavior", Instructions: "Follow current grants.",
	}
}

func configureAgentRuntime(t *testing.T, client agentv1connect.AgentServiceClient, agentID, bindingHandle string) *agentv1.AgentRuntimeSpec {
	t.Helper()
	expectedRevision := uint64(0)
	current, err := client.GetAgentRuntimeSpec(context.Background(), connect.NewRequest(&agentv1.GetAgentRuntimeSpecRequest{AgentId: agentID}))
	if err == nil {
		expectedRevision = current.Msg.GetRuntimeSpec().GetRevision()
	} else if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal(err)
	}
	response, err := client.UpdateAgentRuntimeSpec(context.Background(), connect.NewRequest(&agentv1.UpdateAgentRuntimeSpecRequest{
		RequestId: uuid.NewString(), AgentId: agentID, ExpectedRevision: expectedRevision,
		Engine:           agentv1.EngineKind_ENGINE_KIND_BUILTIN,
		ProviderProtocol: agentv1.ProviderProtocol_PROVIDER_PROTOCOL_OPENAI_RESPONSES,
		ProviderEndpoint: "https://provider.invalid/v1", Model: "test-model",
		CredentialBindingHandle: bindingHandle,
		SandboxProvider:         agentv1.RuntimeSandboxProvider_RUNTIME_SANDBOX_PROVIDER_TRUSTED_LOCAL,
		MaxRunDurationSeconds:   120, MaxOutputBytes: 1 << 20,
		ToolPolicy: &agentv1.RuntimeToolPolicy{Message: true, Work: true, Artifact: true, Knowledge: true},
	}))
	if err != nil {
		t.Fatal(err)
	}
	return response.Msg.GetRuntimeSpec()
}

func prepareServerTestPlacement(
	t *testing.T,
	ownerComputers, computers computerv1connect.ComputerServiceClient,
	agents agentv1connect.AgentServiceClient,
	agentID, computerID, registrationKey string,
) string {
	t.Helper()
	handle := "cred_test_" + agentID + "_" + computerID
	bound := false
	listed, err := ownerComputers.ListCredentialDeliveries(context.Background(), connect.NewRequest(&computerv1.ListCredentialDeliveriesRequest{
		ComputerId: computerID, AgentId: agentID,
	}))
	if err != nil {
		t.Fatal(err)
	}
	for _, delivery := range listed.Msg.GetDeliveries() {
		if delivery.GetState() == computerv1.CredentialDeliveryState_CREDENTIAL_DELIVERY_STATE_SUCCEEDED && delivery.GetBindingHandle() == handle {
			bound = true
			break
		}
	}
	if !bound {
		enqueued, err := ownerComputers.EnqueueCredentialDelivery(context.Background(), connect.NewRequest(&computerv1.EnqueueCredentialDeliveryRequest{
			RequestId: uuid.NewString(), ComputerId: computerID, AgentId: agentID,
			CredentialKind: computerv1.CredentialKind_CREDENTIAL_KIND_OPENAI,
			SealedCredential: &computerv1.SealedCredential{
				Algorithm: computerv1.CredentialDeliveryAlgorithm_CREDENTIAL_DELIVERY_ALGORITHM_X25519_XCHACHA20_POLY1305,
				KeyId:     serverTestCredentialKeyID, EphemeralPublicKey: make([]byte, 32), Nonce: make([]byte, 24), Ciphertext: make([]byte, 17),
			},
			ExpiresAt: timestamppb.New(time.Now().Add(5 * time.Minute)),
		}))
		if err != nil {
			t.Fatal(err)
		}
		claimed, err := computers.ClaimCredentialDelivery(context.Background(), connect.NewRequest(&computerv1.ClaimCredentialDeliveryRequest{
			ComputerId: computerID, RegistrationKey: registrationKey,
		}))
		if err != nil {
			t.Fatal(err)
		}
		if claimed.Msg.GetDelivery().GetId() != enqueued.Msg.GetDelivery().GetId() {
			t.Fatalf("claimed credential delivery = %q, want %q", claimed.Msg.GetDelivery().GetId(), enqueued.Msg.GetDelivery().GetId())
		}
		completed, err := computers.CompleteCredentialDelivery(context.Background(), connect.NewRequest(&computerv1.CompleteCredentialDeliveryRequest{
			ComputerId: computerID, RegistrationKey: registrationKey,
			DeliveryId: claimed.Msg.GetDelivery().GetId(), BindingHandle: handle,
		}))
		if err != nil {
			t.Fatal(err)
		}
		if completed.Msg.GetDelivery().GetState() != computerv1.CredentialDeliveryState_CREDENTIAL_DELIVERY_STATE_SUCCEEDED {
			t.Fatalf("completed credential delivery = %+v", completed.Msg.GetDelivery())
		}
	}
	current, err := agents.GetAgentRuntimeSpec(context.Background(), connect.NewRequest(&agentv1.GetAgentRuntimeSpecRequest{AgentId: agentID}))
	if err == nil && current.Msg.GetRuntimeSpec().GetCredentialBindingHandle() == handle {
		return handle
	}
	if err != nil && connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal(err)
	}
	configureAgentRuntime(t, agents, agentID, handle)
	return handle
}
