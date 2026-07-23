package server

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	agentv1 "github.com/abcdlsj/sumi/gen/go/sumi/agent/v1"
	placementv1 "github.com/abcdlsj/sumi/gen/go/sumi/placement/v1"
	spacev1 "github.com/abcdlsj/sumi/gen/go/sumi/space/v1"
	workv1 "github.com/abcdlsj/sumi/gen/go/sumi/work/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/work/v1/workv1connect"
	"github.com/abcdlsj/sumi/internal/authority"
	"github.com/abcdlsj/sumi/internal/store"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
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
	if _, err := client.TransitionWork(context.Background(), connect.NewRequest(transition)); err != nil {
		t.Fatal(err)
	}
	if _, err := client.TransitionWork(context.Background(), connect.NewRequest(transition)); err != nil {
		t.Fatalf("transition replay = %v", err)
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
	if _, err := client.RequestApproval(context.Background(), connect.NewRequest(approvalRequest)); err != nil {
		t.Fatalf("approval replay = %v", err)
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
	if restored.GetWork().GetState() != workv1.WorkState_WORK_STATE_WAITING_APPROVAL || len(restored.GetAssignments()) != 2 || restored.GetAssignments()[0].GetEndedAt() == nil || restored.GetAssignments()[1].GetEndedAt() != nil || len(restored.GetCriterionResults()) != 1 || len(restored.GetApprovals()) != 1 || restored.GetApprovals()[0].GetStatus() != workv1.WorkApprovalStatus_WORK_APPROVAL_STATUS_PENDING || len(restored.GetEvents()) < 6 {
		t.Fatalf("restart detail = %+v", restored)
	}
	page, err := client.ListWorks(context.Background(), connect.NewRequest(&workv1.ListWorksRequest{Limit: 1}))
	if err != nil || len(page.Msg.GetWorks()) != 1 || page.Msg.GetWorks()[0].GetId() != created.Msg.GetWork().GetId() {
		t.Fatalf("work page = %+v, %v", page.Msg, err)
	}

	resolve := &workv1.ResolveApprovalRequest{RequestId: uuid.NewString(), ApprovalId: pending.Msg.GetApproval().GetId(), Decision: workv1.WorkApprovalDecision_WORK_APPROVAL_DECISION_APPROVED}
	if _, err := client.ResolveApproval(context.Background(), connect.NewRequest(resolve)); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ResolveApproval(context.Background(), connect.NewRequest(resolve)); err != nil {
		t.Fatalf("resolve replay = %v", err)
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
	return &workv1.CreateWorkRequest{RequestId: uuid.NewString(), SourceMessageId: source.ID, SourceSpaceId: source.SpaceID, SourceTarget: &spacev1.MessageTarget{Target: &spacev1.MessageTarget_SpaceId{SpaceId: source.Target.ID}}, SourceTargetSequence: source.TargetSequence, Goal: "durable API work", AcceptanceCriteria: []string{"criterion persists"}}
}
