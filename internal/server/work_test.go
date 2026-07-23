package server

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	agentv1 "github.com/abcdlsj/sumi/gen/go/sumi/agent/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/agent/v1/agentv1connect"
	"github.com/abcdlsj/sumi/gen/go/sumi/computer/v1/computerv1connect"
	placementv1 "github.com/abcdlsj/sumi/gen/go/sumi/placement/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/placement/v1/placementv1connect"
	runtimev1 "github.com/abcdlsj/sumi/gen/go/sumi/runtime/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/runtime/v1/runtimev1connect"
	spacev1 "github.com/abcdlsj/sumi/gen/go/sumi/space/v1"
	workv1 "github.com/abcdlsj/sumi/gen/go/sumi/work/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/work/v1/workv1connect"
	"github.com/abcdlsj/sumi/internal/authority"
	"github.com/abcdlsj/sumi/internal/store"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	_ "modernc.org/sqlite"
)

func TestWorkHTTPReplayConflictAndRestartDetail(t *testing.T) {
	dataRoot := t.TempDir()
	api := openFactsAPI(t, dataRoot)
	credential, source := seedWorkHTTPSource(t, api, dataRoot)
	client := workv1connect.NewWorkServiceClient(api.http.Client(), api.http.URL, clientAuthorization(credential))

	create := workCreateRequest(source)
	created, err := client.CreateWork(context.Background(), connect.NewRequest(create))
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := client.CreateWork(context.Background(), connect.NewRequest(create))
	if err != nil || !proto.Equal(created.Msg, replayed.Msg) {
		t.Fatalf("create replay = %+v, %v", replayed, err)
	}
	changedCreate := proto.Clone(create).(*workv1.CreateWorkRequest)
	changedCreate.Goal = "changed payload"
	_, err = client.CreateWork(context.Background(), connect.NewRequest(changedCreate))
	assertConnectCode(t, err, connect.CodeAlreadyExists)

	computerA, agentA, _, registrationKey := createActiveRuntimeBinding(t, api)
	agentB, err := api.agents.CreateAgent(context.Background(), connect.NewRequest(&agentv1.CreateAgentRequest{
		RequestId: uuid.NewString(), Name: "runtime-" + uuid.NewString()[:8], Driver: agentv1.Driver_DRIVER_NATIVE,
	}))
	if err != nil {
		t.Fatal(err)
	}
	placementB, err := api.placements.SetAgentPlacement(context.Background(), connect.NewRequest(&placementv1.SetAgentPlacementRequest{
		RequestId: uuid.NewString(), AgentId: agentB.Msg.GetAgent().GetId(), ComputerId: computerA.GetId(),
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := api.placements.AcknowledgeAgentPlacement(context.Background(), connect.NewRequest(&placementv1.AcknowledgeAgentPlacementRequest{
		ComputerId: computerA.GetId(), RegistrationKey: registrationKey,
		AgentId: agentB.Msg.GetAgent().GetId(), Generation: placementB.Msg.GetPlacement().GetGeneration(),
		Result: placementv1.AcknowledgementResult_ACKNOWLEDGEMENT_RESULT_ACTIVE,
	})); err != nil {
		t.Fatal(err)
	}
	assignA := &workv1.AssignWorkRequest{RequestId: uuid.NewString(), WorkId: created.Msg.GetWork().GetId(), Role: workv1.WorkAssignmentRole_WORK_ASSIGNMENT_ROLE_COORDINATOR, AgentId: agentA.GetId()}
	firstAssignment, err := client.AssignWork(context.Background(), connect.NewRequest(assignA))
	if err != nil {
		t.Fatal(err)
	}
	replayedAssignment, err := client.AssignWork(context.Background(), connect.NewRequest(assignA))
	if err != nil || !proto.Equal(firstAssignment.Msg, replayedAssignment.Msg) {
		t.Fatalf("assign replay = %+v, %v", replayedAssignment, err)
	}
	changedAssign := proto.Clone(assignA).(*workv1.AssignWorkRequest)
	changedAssign.AgentId = agentB.Msg.GetAgent().GetId()
	_, err = client.AssignWork(context.Background(), connect.NewRequest(changedAssign))
	assertConnectCode(t, err, connect.CodeAlreadyExists)
	secondAssignment, err := client.AssignWork(context.Background(), connect.NewRequest(&workv1.AssignWorkRequest{RequestId: uuid.NewString(), WorkId: created.Msg.GetWork().GetId(), Role: workv1.WorkAssignmentRole_WORK_ASSIGNMENT_ROLE_COORDINATOR, AgentId: agentB.Msg.GetAgent().GetId()}))
	if err != nil {
		t.Fatal(err)
	}
	if secondAssignment.Msg.GetAssignment().GetEndedAt() != nil {
		t.Fatalf("replacement assignment ended = %+v", secondAssignment.Msg)
	}

	detail, err := client.GetWork(context.Background(), connect.NewRequest(&workv1.GetWorkRequest{WorkId: created.Msg.GetWork().GetId()}))
	if err != nil || len(detail.Msg.GetDetail().GetAcceptanceCriteria()) != 1 {
		t.Fatalf("detail before transition = %+v, %v", detail.Msg, err)
	}
	criterionID := detail.Msg.GetDetail().GetAcceptanceCriteria()[0].GetId()
	transition := &workv1.TransitionWorkRequest{RequestId: uuid.NewString(), WorkId: created.Msg.GetWork().GetId(), ToState: workv1.WorkState_WORK_STATE_BLOCKED, Reason: "needs approval", CriterionResults: []*workv1.WorkCriterionResultInput{{CriterionId: criterionID, Verdict: workv1.WorkCriterionVerdict_WORK_CRITERION_VERDICT_PASSED, Evidence: "validated"}}}
	transitioned, err := client.TransitionWork(context.Background(), connect.NewRequest(transition))
	if err != nil {
		t.Fatal(err)
	}
	replayedTransition, err := client.TransitionWork(context.Background(), connect.NewRequest(transition))
	if err != nil || !proto.Equal(transitioned.Msg, replayedTransition.Msg) {
		t.Fatalf("transition replay = %+v, %v", replayedTransition, err)
	}
	changedTransition := proto.Clone(transition).(*workv1.TransitionWorkRequest)
	changedTransition.Reason = "changed"
	_, err = client.TransitionWork(context.Background(), connect.NewRequest(changedTransition))
	assertConnectCode(t, err, connect.CodeAlreadyExists)

	approvalRequest := &workv1.RequestApprovalRequest{RequestId: uuid.NewString(), WorkId: created.Msg.GetWork().GetId(), Question: "approve restart evidence?"}
	pending, err := client.RequestApproval(context.Background(), connect.NewRequest(approvalRequest))
	if err != nil {
		t.Fatal(err)
	}
	replayedApproval, err := client.RequestApproval(context.Background(), connect.NewRequest(approvalRequest))
	if err != nil || !proto.Equal(pending.Msg, replayedApproval.Msg) {
		t.Fatalf("approval replay = %+v, %v", replayedApproval, err)
	}
	changedApproval := proto.Clone(approvalRequest).(*workv1.RequestApprovalRequest)
	changedApproval.Question = "changed"
	_, err = client.RequestApproval(context.Background(), connect.NewRequest(changedApproval))
	assertConnectCode(t, err, connect.CodeAlreadyExists)

	api.close(t)
	api = openFactsAPI(t, dataRoot)
	defer api.close(t)
	client = workv1connect.NewWorkServiceClient(api.http.Client(), api.http.URL, ownerClientAuthorization(t, dataRoot))
	detail, err = client.GetWork(context.Background(), connect.NewRequest(&workv1.GetWorkRequest{WorkId: created.Msg.GetWork().GetId()}))
	if err != nil {
		t.Fatal(err)
	}
	restored := detail.Msg.GetDetail()
	assertWorkRestartDetail(t, restored, create, created.Msg.GetWork(), firstAssignment.Msg.GetAssignment(), secondAssignment.Msg.GetAssignment(), pending.Msg.GetApproval(), transitioned.Msg.GetWork())
	page, err := client.ListWorks(context.Background(), connect.NewRequest(&workv1.ListWorksRequest{Limit: 1}))
	if err != nil || len(page.Msg.GetWorks()) != 1 || page.Msg.GetWorks()[0].GetId() != created.Msg.GetWork().GetId() {
		t.Fatalf("work page = %+v, %v", page.Msg, err)
	}

	resolve := &workv1.ResolveApprovalRequest{RequestId: uuid.NewString(), ApprovalId: pending.Msg.GetApproval().GetId(), Decision: workv1.WorkApprovalDecision_WORK_APPROVAL_DECISION_APPROVED}
	resolved, err := client.ResolveApproval(context.Background(), connect.NewRequest(resolve))
	if err != nil {
		t.Fatal(err)
	}
	replayedResolve, err := client.ResolveApproval(context.Background(), connect.NewRequest(resolve))
	if err != nil || !proto.Equal(resolved.Msg, replayedResolve.Msg) {
		t.Fatalf("resolve replay = %+v, %v", replayedResolve, err)
	}
	changedResolve := proto.Clone(resolve).(*workv1.ResolveApprovalRequest)
	changedResolve.Note = "changed"
	_, err = client.ResolveApproval(context.Background(), connect.NewRequest(changedResolve))
	assertConnectCode(t, err, connect.CodeAlreadyExists)
}

func TestWorkHTTPRejectsUnauthenticatedCursorAndInvalidInput(t *testing.T) {
	dataRoot := t.TempDir()
	api := openFactsAPI(t, dataRoot)
	defer api.close(t)
	client := workv1connect.NewWorkServiceClient(api.http.Client(), api.http.URL)
	_, err := client.ListWorks(context.Background(), connect.NewRequest(&workv1.ListWorksRequest{}))
	assertConnectCode(t, err, connect.CodeUnauthenticated)
	owner := workv1connect.NewWorkServiceClient(api.http.Client(), api.http.URL, ownerClientAuthorization(t, dataRoot))
	_, err = owner.ListWorks(context.Background(), connect.NewRequest(&workv1.ListWorksRequest{Cursor: "broken"}))
	assertConnectCode(t, err, connect.CodeFailedPrecondition)
	if err == nil || err.Error() != "failed_precondition: cursor unavailable" {
		t.Fatalf("cursor error = %v", err)
	}
	_, err = owner.GetWork(context.Background(), connect.NewRequest(&workv1.GetWorkRequest{WorkId: "bad"}))
	assertConnectCode(t, err, connect.CodeInvalidArgument)
}

func TestWorkHTTPMutationAuthenticationLeavesFactsUntouched(t *testing.T) {
	dataRoot := t.TempDir()
	api, origin := openWorkBrowserFactsAPI(t, dataRoot)
	defer api.close(t)
	credential, source := seedWorkHTTPSource(t, api, dataRoot)
	owner := workv1connect.NewWorkServiceClient(api.http.Client(), api.http.URL, clientAuthorization(credential))
	created, err := owner.CreateWork(context.Background(), connect.NewRequest(workCreateRequest(source)))
	if err != nil {
		t.Fatal(err)
	}
	computer, agent, placement, registrationKey := createActiveRuntimeBinding(t, api)
	runtimeClient := runtimev1connect.NewAgentRuntimeServiceClient(api.http.Client(), api.http.URL)
	stale := createRuntimeOverHTTP(t, runtimeClient, computer.GetId(), registrationKey, agent.GetId(), placement.GetGeneration())
	current := createRuntimeOverHTTP(t, runtimeClient, computer.GetId(), registrationKey, agent.GetId(), placement.GetGeneration())
	bootstrap, err := api.app.store.EnsureAuthority(context.Background(), credential, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	ownerPrincipal := store.Principal{Kind: "human", ID: bootstrap.Human.ID, OrganizationID: bootstrap.Organization.ID}
	runtimePrincipal := store.Principal{Kind: "agent", ID: agent.GetId(), OrganizationID: bootstrap.Organization.ID}
	for _, grant := range []struct {
		capability store.Capability
		scope      store.Scope
	}{
		{store.CapabilityWorkManage, store.Scope{Kind: "work", ID: created.Msg.GetWork().GetId()}},
	} {
		if _, err := api.app.store.IssueGrant(context.Background(), store.IssueGrantParams{RequestID: uuid.NewString(), Actor: ownerPrincipal, Subject: runtimePrincipal, Capability: grant.capability, Scope: grant.scope, ParentGrantID: bootstrap.RootGrant.ID, Now: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	runtimeWork := workv1connect.NewWorkServiceClient(api.http.Client(), api.http.URL)
	if _, err := runtimeWork.AssignWork(context.Background(), runtimeRequest(&workv1.AssignWorkRequest{RequestId: uuid.NewString(), WorkId: created.Msg.GetWork().GetId(), AgentId: agent.GetId(), Role: workv1.WorkAssignmentRole_WORK_ASSIGNMENT_ROLE_COORDINATOR}, current.GetToken())); err != nil {
		t.Fatalf("current runtime assign: %v", err)
	}
	if _, err := runtimeWork.TransitionWork(context.Background(), runtimeRequest(&workv1.TransitionWorkRequest{RequestId: uuid.NewString(), WorkId: created.Msg.GetWork().GetId(), ToState: workv1.WorkState_WORK_STATE_BLOCKED, Reason: "runtime transition"}, current.GetToken())); err != nil {
		t.Fatalf("current runtime transition: %v", err)
	}
	pending, err := runtimeWork.RequestApproval(context.Background(), runtimeRequest(&workv1.RequestApprovalRequest{RequestId: uuid.NewString(), WorkId: created.Msg.GetWork().GetId(), Question: "runtime approval"}, current.GetToken()))
	if err != nil {
		t.Fatalf("current runtime approval request: %v", err)
	}
	if _, err := owner.ResolveApproval(context.Background(), connect.NewRequest(&workv1.ResolveApprovalRequest{RequestId: uuid.NewString(), ApprovalId: pending.Msg.GetApproval().GetId(), Decision: workv1.WorkApprovalDecision_WORK_APPROVAL_DECISION_APPROVED})); err != nil {
		t.Fatalf("human approval resolve: %v", err)
	}
	revoke := runtimeRequest(&runtimev1.RevokeAgentRuntimeSessionRequest{ComputerId: computer.GetId(), RegistrationKey: registrationKey}, current.GetToken())
	if _, err := runtimeClient.RevokeAgentRuntimeSession(context.Background(), revoke); err != nil {
		t.Fatal(err)
	}
	browser := browserClient(t, origin, credential)
	browserWork := workv1connect.NewWorkServiceClient(browser, origin)
	plain := workv1connect.NewWorkServiceClient(api.http.Client(), api.http.URL)
	baseline := readWorkHTTPMutationCounts(t, dataRoot)
	cases := []struct {
		name   string
		client workv1connect.WorkServiceClient
		header http.Header
		code   connect.Code
	}{
		{"stale runtime", plain, http.Header{"Authorization": {"Bearer " + stale.GetToken()}}, connect.CodeUnauthenticated},
		{"revoked runtime", plain, http.Header{"Authorization": {"Bearer " + current.GetToken()}}, connect.CodeUnauthenticated},
		{"mixed bearer cookie", plain, http.Header{"Authorization": {"Bearer " + stale.GetToken()}, "Cookie": {"sumi_browser_session=" + strings.Repeat("b", 43)}}, connect.CodeUnauthenticated},
		{"browser missing origin", browserWork, http.Header{}, connect.CodePermissionDenied},
		{"browser bad origin", browserWork, http.Header{"Origin": {"http://localhost:18080"}}, connect.CodePermissionDenied},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			for _, err := range workMutationErrors(test.client, test.header, created.Msg.GetWork().GetId(), agent.GetId(), pending.Msg.GetApproval().GetId()) {
				if connect.CodeOf(err) != test.code {
					t.Fatalf("mutation error = %v, want %s", err, test.code)
				}
				assertWorkHTTPMutationCounts(t, dataRoot, baseline)
			}
		})
	}
}

func workMutationErrors(client workv1connect.WorkServiceClient, header http.Header, workID, agentID, approvalID string) []error {
	create := connect.NewRequest(&workv1.CreateWorkRequest{RequestId: uuid.NewString(), SourceMessageId: uuid.NewString(), SourceSpaceId: uuid.NewString(), SourceTarget: &spacev1.MessageTarget{Target: &spacev1.MessageTarget_SpaceId{SpaceId: uuid.NewString()}}, SourceTargetSequence: 1, Goal: "parseable rejected work", AcceptanceCriteria: []string{"criterion"}})
	assign := connect.NewRequest(&workv1.AssignWorkRequest{RequestId: uuid.NewString(), WorkId: workID, AgentId: agentID, Role: workv1.WorkAssignmentRole_WORK_ASSIGNMENT_ROLE_COORDINATOR})
	transition := connect.NewRequest(&workv1.TransitionWorkRequest{RequestId: uuid.NewString(), WorkId: workID, ToState: workv1.WorkState_WORK_STATE_BLOCKED, Reason: "parseable rejected transition"})
	requestApproval := connect.NewRequest(&workv1.RequestApprovalRequest{RequestId: uuid.NewString(), WorkId: workID, Question: "parseable rejected approval"})
	resolve := connect.NewRequest(&workv1.ResolveApprovalRequest{RequestId: uuid.NewString(), ApprovalId: approvalID, Decision: workv1.WorkApprovalDecision_WORK_APPROVAL_DECISION_APPROVED})
	requests := []connect.AnyRequest{create, assign, transition, requestApproval, resolve}
	for _, request := range requests {
		for key, values := range header {
			request.Header()[key] = append([]string(nil), values...)
		}
	}
	_, createErr := client.CreateWork(context.Background(), create)
	_, assignErr := client.AssignWork(context.Background(), assign)
	_, transitionErr := client.TransitionWork(context.Background(), transition)
	_, requestErr := client.RequestApproval(context.Background(), requestApproval)
	_, resolveErr := client.ResolveApproval(context.Background(), resolve)
	return []error{createErr, assignErr, transitionErr, requestErr, resolveErr}
}

type workHTTPMutationCounts struct {
	works, assignments, approvals, results, events, receipts, dirty int
}

func readWorkHTTPMutationCounts(t *testing.T, dataRoot string) workHTTPMutationCounts {
	t.Helper()
	database, err := sql.Open("sqlite", filepath.Join(dataRoot, "data", "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var counts workHTTPMutationCounts
	queries := []struct {
		query string
		value *int
	}{
		{`SELECT count(*) FROM works`, &counts.works},
		{`SELECT count(*) FROM work_assignments`, &counts.assignments},
		{`SELECT count(*) FROM work_approvals`, &counts.approvals},
		{`SELECT count(*) FROM work_acceptance_results`, &counts.results},
		{`SELECT count(*) FROM work_events`, &counts.events},
		{`SELECT count(*) FROM work_requests`, &counts.receipts},
		{`SELECT count(*) FROM knowledge_dirty_sources WHERE source_kind = 'work'`, &counts.dirty},
	}
	for _, query := range queries {
		if err := database.QueryRow(query.query).Scan(query.value); err != nil {
			t.Fatal(err)
		}
	}
	return counts
}

func assertWorkHTTPMutationCounts(t *testing.T, dataRoot string, want workHTTPMutationCounts) {
	t.Helper()
	if got := readWorkHTTPMutationCounts(t, dataRoot); got != want {
		t.Fatalf("work mutation counts = %+v, want %+v", got, want)
	}
}

func openWorkBrowserFactsAPI(t *testing.T, dataRoot string) (*factsAPI, string) {
	t.Helper()
	httpServer := httptest.NewUnstartedServer(nil)
	origin := "http://" + httpServer.Listener.Addr().String()
	app, err := New(context.Background(), Config{DataRoot: dataRoot, BrowserOrigin: origin})
	if err != nil {
		httpServer.Close()
		t.Fatal(err)
	}
	httpServer.Config.Handler = app.Handler()
	httpServer.Start()
	authorization := ownerClientAuthorization(t, dataRoot)
	return &factsAPI{
		app:       app,
		http:      httpServer,
		computers: computerv1connect.NewComputerServiceClient(httpServer.Client(), httpServer.URL),
		ownerComputers: computerv1connect.NewComputerServiceClient(
			httpServer.Client(), httpServer.URL, authorization,
		),
		agents:     agentv1connect.NewAgentServiceClient(httpServer.Client(), httpServer.URL, authorization),
		placements: placementv1connect.NewPlacementServiceClient(httpServer.Client(), httpServer.URL, authorization),
	}, origin
}

func seedWorkHTTPSource(t *testing.T, api *factsAPI, dataRoot string) (string, store.Message) {
	t.Helper()
	credential, err := authority.ReadCredentialFile(filepath.Join(dataRoot, "owner.key"))
	if err != nil {
		t.Fatal(err)
	}
	bootstrap, err := api.app.store.EnsureAuthority(context.Background(), credential, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	owner := store.Principal{Kind: "human", ID: bootstrap.Human.ID, OrganizationID: bootstrap.Organization.ID}
	group, err := api.app.store.CreateGroup(context.Background(), store.CreateGroupParams{RequestID: uuid.NewString(), Actor: owner, Name: "Work HTTP", Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	message, err := api.app.store.SendMessage(context.Background(), store.SendMessageParams{RequestID: uuid.NewString(), Actor: owner, Target: store.MessageTarget{Kind: store.MessageTargetSpace, ID: group.ID}, Body: "work source", Now: time.Now().Add(time.Nanosecond)})
	if err != nil {
		t.Fatal(err)
	}
	return credential, message
}

func workCreateRequest(source store.Message) *workv1.CreateWorkRequest {
	return &workv1.CreateWorkRequest{RequestId: uuid.NewString(), SourceMessageId: source.ID, SourceSpaceId: source.SpaceID, SourceTarget: &spacev1.MessageTarget{Target: &spacev1.MessageTarget_SpaceId{SpaceId: source.Target.ID}}, SourceTargetSequence: source.TargetSequence, Goal: "durable API work", Constraints: []string{"constraint persists"}, AcceptanceCriteria: []string{"criterion persists"}}
}

func assertWorkRestartDetail(t *testing.T, detail *workv1.WorkDetail, create *workv1.CreateWorkRequest, created *workv1.Work, first, second *workv1.WorkAssignment, pending *workv1.WorkApproval, transitioned *workv1.Work) {
	t.Helper()
	work := detail.GetWork()
	if work.GetId() != created.GetId() || work.GetOrganizationId() != created.GetOrganizationId() || work.GetRootWorkId() != created.GetRootWorkId() || work.GetParentWorkId() != created.GetParentWorkId() || work.GetSourceMessageId() != created.GetSourceMessageId() || work.GetSourceSpaceId() != created.GetSourceSpaceId() || !proto.Equal(work.GetSourceTarget(), created.GetSourceTarget()) || work.GetSourceTargetSequence() != created.GetSourceTargetSequence() || work.GetTeamSpaceId() != created.GetTeamSpaceId() || work.GetGoal() != create.GetGoal() || work.GetState() != workv1.WorkState_WORK_STATE_WAITING_APPROVAL || work.GetBlockingReason() != "" || work.GetResult() != "" || !proto.Equal(work.GetCreator(), created.GetCreator()) || !work.GetCreatedAt().AsTime().Equal(created.GetCreatedAt().AsTime()) || work.GetUpdatedAt() == nil || work.GetStateChangedAt() == nil || work.GetCompletedAt() != nil || work.GetFailedAt() != nil || work.GetCancelledAt() != nil || work.GetUpdatedAt().AsTime().Before(transitioned.GetUpdatedAt().AsTime()) || work.GetStateChangedAt().AsTime().Before(transitioned.GetStateChangedAt().AsTime()) {
		t.Fatalf("restart work projection = %+v", work)
	}
	if len(detail.GetConstraints()) != 1 || detail.GetConstraints()[0].GetId() == "" || detail.GetConstraints()[0].GetOrdinal() != 0 || detail.GetConstraints()[0].GetBody() != create.GetConstraints()[0] || detail.GetConstraints()[0].GetCreatedAt() == nil {
		t.Fatalf("restart constraints projection = %+v", detail.GetConstraints())
	}
	if len(detail.GetAcceptanceCriteria()) != 1 || detail.GetAcceptanceCriteria()[0].GetId() == "" || detail.GetAcceptanceCriteria()[0].GetOrdinal() != 0 || detail.GetAcceptanceCriteria()[0].GetBody() != create.GetAcceptanceCriteria()[0] || detail.GetAcceptanceCriteria()[0].GetCreatedAt() == nil {
		t.Fatalf("restart criteria projection = %+v", detail.GetAcceptanceCriteria())
	}
	if len(detail.GetAssignments()) != 2 {
		t.Fatalf("restart assignments projection = %+v", detail.GetAssignments())
	}
	assertWorkAssignmentProjection(t, detail.GetAssignments()[0], first, true)
	assertWorkAssignmentProjection(t, detail.GetAssignments()[1], second, false)
	if len(detail.GetApprovals()) != 1 || detail.GetApprovals()[0].GetId() != pending.GetId() || detail.GetApprovals()[0].GetWorkId() != pending.GetWorkId() || detail.GetApprovals()[0].GetOrganizationId() != pending.GetOrganizationId() || detail.GetApprovals()[0].GetStatus() != workv1.WorkApprovalStatus_WORK_APPROVAL_STATUS_PENDING || detail.GetApprovals()[0].GetQuestion() != "approve restart evidence?" || !proto.Equal(detail.GetApprovals()[0].GetRequestedBy(), created.GetCreator()) || detail.GetApprovals()[0].GetRequestedAt() == nil || detail.GetApprovals()[0].GetDecidedByHumanId() != "" || detail.GetApprovals()[0].GetDecisionNote() != "" || detail.GetApprovals()[0].GetDecidedAt() != nil {
		t.Fatalf("restart approval projection = %+v", detail.GetApprovals())
	}
	if len(detail.GetCriterionResults()) != 1 {
		t.Fatalf("restart criterion result projection = %+v", detail.GetCriterionResults())
	}
	result := detail.GetCriterionResults()[0]
	if result.GetSequence() == 0 || result.GetId() == "" || result.GetWorkId() != created.GetId() || result.GetOrganizationId() != created.GetOrganizationId() || result.GetCriterionId() != detail.GetAcceptanceCriteria()[0].GetId() || result.GetVerdict() != workv1.WorkCriterionVerdict_WORK_CRITERION_VERDICT_PASSED || result.GetEvidence() != "validated" || !proto.Equal(result.GetActor(), created.GetCreator()) || result.GetOccurredAt() == nil {
		t.Fatalf("restart criterion result projection = %+v", result)
	}
	wantEvents := []struct {
		kind, referenceKind, referenceID, reason string
		from, to                                 workv1.WorkState
	}{
		{"created", "", "", "", workv1.WorkState_WORK_STATE_UNSPECIFIED, workv1.WorkState_WORK_STATE_UNSPECIFIED},
		{"assignment.started", "assignment", first.GetId(), "", workv1.WorkState_WORK_STATE_UNSPECIFIED, workv1.WorkState_WORK_STATE_UNSPECIFIED},
		{"assignment.ended", "assignment", first.GetId(), "reassigned", workv1.WorkState_WORK_STATE_UNSPECIFIED, workv1.WorkState_WORK_STATE_UNSPECIFIED},
		{"assignment.started", "assignment", second.GetId(), "", workv1.WorkState_WORK_STATE_UNSPECIFIED, workv1.WorkState_WORK_STATE_UNSPECIFIED},
		{"acceptance.recorded", "criterion_result", result.GetId(), "", workv1.WorkState_WORK_STATE_UNSPECIFIED, workv1.WorkState_WORK_STATE_UNSPECIFIED},
		{"state.transitioned", "", "", "needs approval", workv1.WorkState_WORK_STATE_OPEN, workv1.WorkState_WORK_STATE_BLOCKED},
		{"approval.requested", "approval", pending.GetId(), "approve restart evidence?", workv1.WorkState_WORK_STATE_UNSPECIFIED, workv1.WorkState_WORK_STATE_UNSPECIFIED},
		{"state.transitioned", "approval", pending.GetId(), "", workv1.WorkState_WORK_STATE_BLOCKED, workv1.WorkState_WORK_STATE_WAITING_APPROVAL},
	}
	if len(detail.GetEvents()) != len(wantEvents) {
		t.Fatalf("restart event count = %+v", detail.GetEvents())
	}
	for index, want := range wantEvents {
		event := detail.GetEvents()[index]
		if event.GetSequence() != uint64(index+1) || event.GetId() == "" || event.GetWorkId() != created.GetId() || event.GetOrganizationId() != created.GetOrganizationId() || event.GetKind() != want.kind || !proto.Equal(event.GetActor(), created.GetCreator()) || event.GetFromState() != want.from || event.GetToState() != want.to || event.GetReferenceKind() != want.referenceKind || event.GetReferenceId() != want.referenceID || event.GetReason() != want.reason || event.GetOccurredAt() == nil {
			t.Fatalf("restart event[%d] projection = %+v, want %+v", index, event, want)
		}
	}
}

func assertWorkAssignmentProjection(t *testing.T, actual, expected *workv1.WorkAssignment, ended bool) {
	t.Helper()
	if actual.GetId() != expected.GetId() || actual.GetWorkId() != expected.GetWorkId() || actual.GetOrganizationId() != expected.GetOrganizationId() || actual.GetRole() != expected.GetRole() || actual.GetAgentId() != expected.GetAgentId() || actual.GetHolderComputerId() != expected.GetHolderComputerId() || actual.GetHolderPlacementGeneration() != expected.GetHolderPlacementGeneration() || !proto.Equal(actual.GetAssignedBy(), expected.GetAssignedBy()) || actual.GetAssignedAt() == nil {
		t.Fatalf("assignment projection = %+v, want %+v", actual, expected)
	}
	if ended {
		if actual.GetEndedAt() == nil || actual.GetEndReason() != "reassigned" {
			t.Fatalf("ended assignment projection = %+v", actual)
		}
		return
	}
	if actual.GetEndedAt() != nil || actual.GetEndReason() != "" {
		t.Fatalf("active assignment projection = %+v", actual)
	}
}
