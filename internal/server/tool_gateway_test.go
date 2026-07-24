package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	agentv1 "github.com/abcdlsj/sumi/gen/go/sumi/agent/v1"
	artifactv1 "github.com/abcdlsj/sumi/gen/go/sumi/artifact/v1"
	grantv1 "github.com/abcdlsj/sumi/gen/go/sumi/grant/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/grant/v1/grantv1connect"
	"github.com/abcdlsj/sumi/gen/go/sumi/inbox/v1/inboxv1connect"
	runv1 "github.com/abcdlsj/sumi/gen/go/sumi/run/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/run/v1/runv1connect"
	"github.com/abcdlsj/sumi/gen/go/sumi/runtime/v1/runtimev1connect"
	spacev1 "github.com/abcdlsj/sumi/gen/go/sumi/space/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/space/v1/spacev1connect"
	workv1 "github.com/abcdlsj/sumi/gen/go/sumi/work/v1"
	computerstate "github.com/abcdlsj/sumi/internal/computer/state"
	"github.com/abcdlsj/sumi/internal/provider"
	"github.com/abcdlsj/sumi/internal/tool"
	toolremote "github.com/abcdlsj/sumi/internal/tool/remote"
	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestRemoteToolGatewayUsesCurrentRuntimeAndExactRunTarget(t *testing.T) {
	dataRoot := t.TempDir()
	api := openFactsAPI(t, dataRoot)
	defer api.close(t)
	computer, agent, placement, registrationKey := createActiveRuntimeBinding(t, api)
	runtimeClient := runtimev1connect.NewRuntimeServiceClient(api.http.Client(), api.http.URL)
	session := createRuntimeOverHTTP(t, runtimeClient, computer.GetId(), registrationKey, agent.GetId(), placement.GetDesiredRevision())

	ownerOption := ownerClientAuthorization(t, dataRoot)
	grants := grantv1connect.NewGrantServiceClient(api.http.Client(), api.http.URL, ownerOption)
	spaces := spacev1connect.NewCollaborationServiceClient(api.http.Client(), api.http.URL, ownerOption)
	rootGrantID := rootGrantOverHTTP(t, grants)
	groupResponse, err := spaces.CreateGroup(context.Background(), connect.NewRequest(&spacev1.CreateGroupRequest{
		RequestId: uuid.NewString(), Name: "Remote Tool Gateway",
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
	trigger := sendMention(t, spaces, group.GetId(), agent.GetId(), "execute through tools")
	inbox := inboxv1connect.NewInboxServiceClient(api.http.Client(), api.http.URL)
	item := findInboxItem(t, inbox, session.GetToken(), trigger.GetId())
	observeInbox(t, inbox, session.GetToken(), item.GetTarget())
	runs := runv1connect.NewRunServiceClient(api.http.Client(), api.http.URL)
	listed, err := runs.ListRuns(context.Background(), runtimeRequestHTTP(session.GetToken(), &runv1.ListRunsRequest{Limit: 20}))
	if err != nil || len(listed.Msg.GetRuns()) != 1 {
		t.Fatalf("queued runs = %+v, %v", listed, err)
	}
	claimed, err := runs.ClaimRun(context.Background(), runtimeRequestHTTP(session.GetToken(), &runv1.ClaimRunRequest{
		RequestId: uuid.NewString(), RunId: listed.Msg.GetRuns()[0].GetId(),
	}))
	if err != nil {
		t.Fatal(err)
	}
	run := claimed.Msg.GetRun()

	state, err := computerstate.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	saveRemoteRuntimeSession(t, state, session.GetToken(), session.GetExpiresAt().AsTime(), agent.GetId(), computer.GetId(), placement.GetDesiredRevision())
	gateway, err := toolremote.NewGateway(toolremote.Config{
		ServerURL: api.http.URL, HTTPClient: api.http.Client(), State: state,
		AgentID: agent.GetId(), ComputerID: computer.GetId(), PlacementDesiredRevision: placement.GetDesiredRevision(),
		ToolPolicy: &agentv1.RuntimeToolPolicy{Message: true}, Timeout: time.Second,
		MaxCallsPerRun: 8, MaxArgumentBytes: 4096, MaxResultBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	runContext := tool.RunContext{
		AgentID: agent.GetId(), ComputerID: computer.GetId(), RunID: run.GetId(),
		Attempt: run.GetAttempt(), Fence: run.GetFence(), PlacementDesiredRevision: run.GetPlacementDesiredRevision(),
	}
	first := provider.ToolCall{ID: "call-1", Name: "sumi_message_send", Arguments: []byte(`{"body":"first tool message"}`)}
	firstResult, err := gateway.Execute(context.Background(), runContext, first)
	if err != nil {
		t.Fatal(err)
	}
	var firstResponse spacev1.SendMessageResponse
	if err := protojson.Unmarshal(firstResult, &firstResponse); err != nil {
		t.Fatal(err)
	}
	if firstResponse.GetMessage().GetBody() != "first tool message" || firstResponse.GetMessage().GetSpaceId() != group.GetId() || firstResponse.GetMessage().GetThreadRootMessageId() != "" {
		t.Fatalf("first tool message = %+v", firstResponse.GetMessage())
	}
	replayed, err := gateway.Execute(context.Background(), runContext, first)
	if err != nil || !bytes.Equal(replayed, firstResult) {
		t.Fatalf("tool replay = %s, %v", replayed, err)
	}

	rotated := createRuntimeOverHTTP(t, runtimeClient, computer.GetId(), registrationKey, agent.GetId(), placement.GetDesiredRevision())
	saveRemoteRuntimeSession(t, state, rotated.GetToken(), rotated.GetExpiresAt().AsTime(), agent.GetId(), computer.GetId(), placement.GetDesiredRevision())
	secondResult, err := gateway.Execute(context.Background(), runContext, provider.ToolCall{
		ID: "call-2", Name: "sumi_message_send", Arguments: []byte(`{"body":"second tool message"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	var secondResponse spacev1.SendMessageResponse
	if err := protojson.Unmarshal(secondResult, &secondResponse); err != nil {
		t.Fatal(err)
	}
	if secondResponse.GetMessage().GetBody() != "second tool message" || secondResponse.GetMessage().GetSpaceId() != group.GetId() || secondResponse.GetMessage().GetThreadRootMessageId() != "" {
		t.Fatalf("second tool message = %+v", secondResponse.GetMessage())
	}

	if _, err := runs.CancelRun(context.Background(), runtimeRequestHTTP(rotated.GetToken(), &runv1.CancelRunRequest{
		RequestId: uuid.NewString(), RunId: run.GetId(),
		RunProof: &grantv1.RunProof{RunId: run.GetId(), Attempt: run.GetAttempt(), Fence: run.GetFence()},
	})); err != nil {
		t.Fatal(err)
	}
	before, err := spaces.ListMessages(context.Background(), connect.NewRequest(&spacev1.ListMessagesRequest{Target: item.GetTarget(), Limit: 200}))
	if err != nil {
		t.Fatal(err)
	}
	runtimeSpaces := spacev1connect.NewCollaborationServiceClient(api.http.Client(), api.http.URL)
	_, err = runtimeSpaces.SendMessage(context.Background(), runtimeRequestHTTP(rotated.GetToken(), &spacev1.SendMessageRequest{
		RequestId: uuid.NewString(), Target: item.GetTarget(), Body: "stale transaction message",
		RunProof: &grantv1.RunProof{RunId: run.GetId(), Attempt: run.GetAttempt(), Fence: run.GetFence()},
	}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("stale transactional Run proof error = %v", err)
	}
	after, err := spaces.ListMessages(context.Background(), connect.NewRequest(&spacev1.ListMessagesRequest{Target: item.GetTarget(), Limit: 200}))
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Msg.GetMessages()) != len(before.Msg.GetMessages()) {
		t.Fatalf("stale Run proof persisted a message: before %d after %d", len(before.Msg.GetMessages()), len(after.Msg.GetMessages()))
	}
	_, err = gateway.Execute(context.Background(), runContext, provider.ToolCall{
		ID: "call-3", Name: "sumi_message_send", Arguments: []byte(`{"body":"must be denied"}`),
	})
	if !errors.Is(err, tool.ErrDenied) {
		t.Fatalf("tool call after Run cancellation error = %v", err)
	}
	if result := gateway.Definitions(); len(result) != 1 || result[0].Name != "sumi_message_send" {
		t.Fatalf("tool definitions = %+v", result)
	}
}

func TestRemoteToolGatewayCreatesWorkAndPublishesArtifactWithExactRunProof(t *testing.T) {
	dataRoot := t.TempDir()
	api := openFactsAPI(t, dataRoot)
	defer api.close(t)
	computer, agent, placement, registrationKey := createActiveRuntimeBinding(t, api)
	runtimeClient := runtimev1connect.NewRuntimeServiceClient(api.http.Client(), api.http.URL)
	session := createRuntimeOverHTTP(t, runtimeClient, computer.GetId(), registrationKey, agent.GetId(), placement.GetDesiredRevision())

	ownerOption := ownerClientAuthorization(t, dataRoot)
	grants := grantv1connect.NewGrantServiceClient(api.http.Client(), api.http.URL, ownerOption)
	spaces := spacev1connect.NewCollaborationServiceClient(api.http.Client(), api.http.URL, ownerOption)
	rootGrantID := rootGrantOverHTTP(t, grants)
	groupResponse, err := spaces.CreateGroup(context.Background(), connect.NewRequest(&spacev1.CreateGroupRequest{
		RequestId: uuid.NewString(), Name: "Remote Work Tools",
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
		{grantv1.Capability_CAPABILITY_RUN_EXECUTE, &grantv1.Scope{Kind: grantv1.ScopeKind_SCOPE_KIND_AGENT, Id: agent.GetId()}},
		{grantv1.Capability_CAPABILITY_WORK_CREATE, &grantv1.Scope{Kind: grantv1.ScopeKind_SCOPE_KIND_ORGANIZATION, Id: group.GetOrganizationId()}},
	} {
		issueRunGrantHTTP(t, grants, rootGrantID, agent.GetId(), value.capability, value.scope)
	}
	trigger := sendMention(t, spaces, group.GetId(), agent.GetId(), "turn this into durable Work")
	inbox := inboxv1connect.NewInboxServiceClient(api.http.Client(), api.http.URL)
	item := findInboxItem(t, inbox, session.GetToken(), trigger.GetId())
	observeInbox(t, inbox, session.GetToken(), item.GetTarget())
	runs := runv1connect.NewRunServiceClient(api.http.Client(), api.http.URL)
	listed, err := runs.ListRuns(context.Background(), runtimeRequestHTTP(session.GetToken(), &runv1.ListRunsRequest{Limit: 20}))
	if err != nil || len(listed.Msg.GetRuns()) != 1 {
		t.Fatalf("queued Runs = %+v, %v", listed, err)
	}
	claimed, err := runs.ClaimRun(context.Background(), runtimeRequestHTTP(session.GetToken(), &runv1.ClaimRunRequest{
		RequestId: uuid.NewString(), RunId: listed.Msg.GetRuns()[0].GetId(),
	}))
	if err != nil {
		t.Fatal(err)
	}
	run := claimed.Msg.GetRun()

	state, err := computerstate.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	saveRemoteRuntimeSession(t, state, session.GetToken(), session.GetExpiresAt().AsTime(), agent.GetId(), computer.GetId(), placement.GetDesiredRevision())
	gateway, err := toolremote.NewGateway(toolremote.Config{
		ServerURL: api.http.URL, HTTPClient: api.http.Client(), State: state,
		AgentID: agent.GetId(), ComputerID: computer.GetId(), PlacementDesiredRevision: placement.GetDesiredRevision(),
		ToolPolicy: &agentv1.RuntimeToolPolicy{Work: true, Artifact: true}, Timeout: time.Second,
		MaxCallsPerRun: 8, MaxArgumentBytes: 8192, MaxResultBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	runContext := tool.RunContext{
		AgentID: agent.GetId(), ComputerID: computer.GetId(), RunID: run.GetId(),
		Attempt: run.GetAttempt(), Fence: run.GetFence(), PlacementDesiredRevision: run.GetPlacementDesiredRevision(),
	}
	createdPayload, err := gateway.Execute(context.Background(), runContext, provider.ToolCall{
		ID: "work-create", Name: "sumi_work_create",
		Arguments: []byte(`{"goal":"ship a durable result","constraints":["keep evidence"],"acceptance_criteria":["result is reviewed"]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	var created workv1.CreateWorkResponse
	if err := protojson.Unmarshal(createdPayload, &created); err != nil {
		t.Fatal(err)
	}
	workID := created.GetWork().GetId()
	if workID == "" || created.GetWork().GetSourceMessageId() != trigger.GetId() {
		t.Fatalf("created Work = %+v", created.GetWork())
	}
	issueRunGrantHTTP(t, grants, rootGrantID, agent.GetId(), grantv1.Capability_CAPABILITY_WORK_MANAGE,
		&grantv1.Scope{Kind: grantv1.ScopeKind_SCOPE_KIND_WORK, Id: workID})
	issueRunGrantHTTP(t, grants, rootGrantID, agent.GetId(), grantv1.Capability_CAPABILITY_WORK_READ,
		&grantv1.Scope{Kind: grantv1.ScopeKind_SCOPE_KIND_WORK, Id: workID})

	assignedPayload, err := gateway.Execute(context.Background(), runContext, provider.ToolCall{
		ID: "work-assign", Name: "sumi_work_assign",
		Arguments: []byte(`{"work_id":"` + workID + `","agent_id":"` + agent.GetId() + `","role":"coordinator"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	var assigned workv1.AssignWorkResponse
	if err := protojson.Unmarshal(assignedPayload, &assigned); err != nil {
		t.Fatal(err)
	}
	if assigned.GetAssignment().GetAgentId() != agent.GetId() {
		t.Fatalf("assignment = %+v", assigned.GetAssignment())
	}
	transitionedPayload, err := gateway.Execute(context.Background(), runContext, provider.ToolCall{
		ID: "work-transition", Name: "sumi_work_transition",
		Arguments: []byte(`{"work_id":"` + workID + `","to_state":"blocked","reason":"needs review","result":"","criterion_results":[]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	var transitioned workv1.TransitionWorkResponse
	if err := protojson.Unmarshal(transitionedPayload, &transitioned); err != nil {
		t.Fatal(err)
	}
	if transitioned.GetWork().GetState() != workv1.WorkState_WORK_STATE_BLOCKED {
		t.Fatalf("transitioned Work = %+v", transitioned.GetWork())
	}
	approvalPayload, err := gateway.Execute(context.Background(), runContext, provider.ToolCall{
		ID: "work-approval", Name: "sumi_work_request_approval",
		Arguments: []byte(`{"work_id":"` + workID + `","question":"May I proceed?"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	var approval workv1.RequestApprovalResponse
	if err := protojson.Unmarshal(approvalPayload, &approval); err != nil {
		t.Fatal(err)
	}
	if approval.GetApproval().GetStatus() != workv1.WorkApprovalStatus_WORK_APPROVAL_STATUS_PENDING {
		t.Fatalf("approval = %+v", approval.GetApproval())
	}

	content := []byte("durable artifact from the exact Run")
	publishedPayload, err := gateway.Execute(context.Background(), runContext, provider.ToolCall{
		ID: "artifact-publish", Name: "sumi_artifact_publish",
		Arguments: []byte(`{"owning_work_id":"` + workID + `","name":"run-report","media_type":"text/plain","summary":"durable result","content_base64":"` + base64.StdEncoding.EncodeToString(content) + `"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	var published artifactv1.PublishArtifactResponse
	if err := protojson.Unmarshal(publishedPayload, &published); err != nil {
		t.Fatal(err)
	}
	artifactID := published.GetArtifact().GetId()
	if artifactID == "" || published.GetArtifact().GetOwningWorkId() != workID ||
		published.GetVersion().GetExecution().GetRunId() != run.GetId() ||
		published.GetVersion().GetExecution().GetAttempt() != run.GetAttempt() {
		t.Fatalf("published Artifact = %+v / %+v", published.GetArtifact(), published.GetVersion())
	}

	getPayload, err := gateway.Execute(context.Background(), runContext, provider.ToolCall{
		ID: "artifact-get", Name: "sumi_artifact_get",
		Arguments: []byte(`{"artifact_id":"` + artifactID + `","version":1}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	var got artifactv1.GetArtifactResponse
	if err := protojson.Unmarshal(getPayload, &got); err != nil {
		t.Fatal(err)
	}
	if got.GetView().GetArtifact().GetId() != artifactID || got.GetView().GetVersion().GetVersion() != 1 {
		t.Fatalf("Artifact get = %+v", got.GetView())
	}

	listPayload, err := gateway.Execute(context.Background(), runContext, provider.ToolCall{
		ID: "artifact-list", Name: "sumi_artifact_list",
		Arguments: []byte(`{"owning_work_id":"` + workID + `","limit":20}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	var listedArtifacts artifactv1.ListArtifactsResponse
	if err := protojson.Unmarshal(listPayload, &listedArtifacts); err != nil {
		t.Fatal(err)
	}
	if len(listedArtifacts.GetViews()) != 1 || listedArtifacts.GetViews()[0].GetArtifact().GetId() != artifactID {
		t.Fatalf("Artifact list = %+v", listedArtifacts.GetViews())
	}

	fetchPayload, err := gateway.Execute(context.Background(), runContext, provider.ToolCall{
		ID: "artifact-fetch", Name: "sumi_artifact_fetch",
		Arguments: []byte(`{"artifact_id":"` + artifactID + `","version":1}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	var fetched struct {
		View          json.RawMessage `json:"view"`
		ContentBase64 string          `json:"content_base64"`
	}
	if err := json.Unmarshal(fetchPayload, &fetched); err != nil {
		t.Fatal(err)
	}
	var fetchedView artifactv1.ArtifactView
	if err := protojson.Unmarshal(fetched.View, &fetchedView); err != nil {
		t.Fatal(err)
	}
	fetchedContent, err := base64.StdEncoding.DecodeString(fetched.ContentBase64)
	if err != nil {
		t.Fatal(err)
	}
	if fetchedView.GetArtifact().GetId() != artifactID || !bytes.Equal(fetchedContent, content) {
		t.Fatalf("Artifact fetch = %+v / %q", &fetchedView, fetchedContent)
	}
	definitions := gateway.Definitions()
	if len(definitions) != 9 || definitions[0].Name != "sumi_artifact_fetch" || definitions[4].Name != "sumi_work_assign" {
		t.Fatalf("Work and Artifact tool definitions = %+v", definitions)
	}
}

func saveRemoteRuntimeSession(t *testing.T, state *computerstate.State, token string, expiresAt time.Time, agentID, computerID string, desiredRevision uint64) {
	t.Helper()
	if err := state.SaveRuntimeSession(context.Background(), computerstate.RuntimeSession{
		AgentID: agentID, ComputerID: computerID, PlacementDesiredRevision: desiredRevision,
		Token: token, ExpiresAt: expiresAt, UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
}
