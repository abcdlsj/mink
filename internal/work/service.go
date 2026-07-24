package work

import (
	"context"
	"errors"
	"net/http"
	"time"

	"connectrpc.com/connect"
	grantv1 "github.com/abcdlsj/sumi/gen/go/sumi/grant/v1"
	spacev1 "github.com/abcdlsj/sumi/gen/go/sumi/space/v1"
	workv1 "github.com/abcdlsj/sumi/gen/go/sumi/work/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/work/v1/workv1connect"
	sharedauthentication "github.com/abcdlsj/sumi/internal/authentication"
	authorityapp "github.com/abcdlsj/sumi/internal/authority/application"
	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
	collaborationapp "github.com/abcdlsj/sumi/internal/collaboration/application"
	collaborationdomain "github.com/abcdlsj/sumi/internal/collaboration/domain"
	executionapp "github.com/abcdlsj/sumi/internal/execution/application"
	"github.com/abcdlsj/sumi/internal/transport/connectid"
	workapp "github.com/abcdlsj/sumi/internal/work/application"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type workStore interface {
	sharedauthentication.Authenticator
	GetWorkDetail(context.Context, workapp.ReadQuery) (workapp.Detail, error)
	ListWorkPage(context.Context, workapp.ListQuery) (workapp.Page, error)
	CreateWork(context.Context, workapp.CreateCommand) (workapp.Work, error)
	AssignWork(context.Context, workapp.AssignCommand) (workapp.Assignment, error)
	TransitionWork(context.Context, workapp.TransitionCommand) (workapp.Work, error)
	RequestWorkApproval(context.Context, workapp.RequestApprovalCommand) (workapp.Approval, error)
	ResolveWorkApproval(context.Context, workapp.ResolveApprovalCommand) (workapp.Approval, error)
}

type Service struct {
	store  workStore
	origin string
	now    func() time.Time
}

var _ workv1connect.WorkServiceHandler = (*Service)(nil)

func New(database workStore, browserOrigin string) *Service {
	return &Service{store: database, origin: browserOrigin, now: time.Now}
}

func (s *Service) ListWorks(ctx context.Context, request *connect.Request[workv1.ListWorksRequest]) (*connect.Response[workv1.ListWorksResponse], error) {
	identity, now, err := s.resolve(ctx, request.Header(), false)
	if err != nil {
		return nil, err
	}
	page, err := s.store.ListWorkPage(ctx, workapp.ListQuery{
		Actor: identity.human, Agent: identity.agent, Cursor: request.Msg.GetCursor(), Limit: request.Msg.GetLimit(), Now: now,
	})
	if err != nil {
		return nil, serviceError(err)
	}
	response := &workv1.ListWorksResponse{Works: make([]*workv1.Work, 0, len(page.Works)), NextCursor: page.NextCursor}
	for _, item := range page.Works {
		message, err := workMessage(item)
		if err != nil {
			return nil, err
		}
		response.Works = append(response.Works, message)
	}
	return connect.NewResponse(response), nil
}

func (s *Service) GetWork(ctx context.Context, request *connect.Request[workv1.GetWorkRequest]) (*connect.Response[workv1.GetWorkResponse], error) {
	identity, now, err := s.resolve(ctx, request.Header(), false)
	if err != nil {
		return nil, err
	}
	workID, err := connectid.CanonicalID(request.Msg.GetWorkId(), "work id")
	if err != nil {
		return nil, err
	}
	detail, err := s.store.GetWorkDetail(ctx, workapp.ReadQuery{Actor: identity.human, Agent: identity.agent, WorkID: workID, Now: now})
	if err != nil {
		return nil, serviceError(err)
	}
	message, err := detailMessage(detail)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&workv1.GetWorkResponse{Detail: message}), nil
}

func (s *Service) CreateWork(ctx context.Context, request *connect.Request[workv1.CreateWorkRequest]) (*connect.Response[workv1.CreateWorkResponse], error) {
	identity, now, err := s.mutationIdentity(ctx, request.Header())
	if err != nil {
		return nil, err
	}
	actor := identity.human
	if !actor.Valid() {
		actor = identity.agent.Principal
	}
	params, err := createParams(request.Msg, actor, now)
	if err != nil {
		return nil, err
	}
	params.Agent = identity.agent
	params.Run, err = workRunProof(request.Msg.GetRunId(), request.Msg.GetRunAttempt(), request.Msg.GetRunFence())
	if err != nil {
		return nil, err
	}
	item, err := s.store.CreateWork(ctx, params)
	if err != nil {
		return nil, serviceError(err)
	}
	message, err := workMessage(item)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&workv1.CreateWorkResponse{Work: message}), nil
}

func (s *Service) AssignWork(ctx context.Context, request *connect.Request[workv1.AssignWorkRequest]) (*connect.Response[workv1.AssignWorkResponse], error) {
	identity, now, err := s.mutationIdentity(ctx, request.Header())
	if err != nil {
		return nil, err
	}
	actor := identity.human
	if !actor.Valid() {
		actor = identity.agent.Principal
	}
	requestID, workID, agentID, err := mutationIDs(request.Msg.GetRequestId(), request.Msg.GetWorkId(), "work id", request.Msg.GetAgentId(), "agent id")
	if err != nil {
		return nil, err
	}
	role, err := assignmentRoleValue(request.Msg.GetRole())
	if err != nil {
		return nil, err
	}
	run, err := workRunProof(request.Msg.GetRunId(), request.Msg.GetRunAttempt(), request.Msg.GetRunFence())
	if err != nil {
		return nil, err
	}
	assignment, err := s.store.AssignWork(ctx, workapp.AssignCommand{RequestID: requestID, Actor: actor, Agent: identity.agent, Run: run, WorkID: workID, Role: role, AgentID: agentID, Now: now})
	if err != nil {
		return nil, serviceError(err)
	}
	message, err := assignmentMessage(assignment)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&workv1.AssignWorkResponse{Assignment: message}), nil
}

func (s *Service) TransitionWork(ctx context.Context, request *connect.Request[workv1.TransitionWorkRequest]) (*connect.Response[workv1.TransitionWorkResponse], error) {
	identity, now, err := s.mutationIdentity(ctx, request.Header())
	if err != nil {
		return nil, err
	}
	actor := identity.human
	if !actor.Valid() {
		actor = identity.agent.Principal
	}
	requestID, workID, _, err := mutationIDs(request.Msg.GetRequestId(), request.Msg.GetWorkId(), "work id", "", "")
	if err != nil {
		return nil, err
	}
	state, err := workStateValue(request.Msg.GetToState())
	if err != nil {
		return nil, err
	}
	results, err := criterionResults(request.Msg.GetCriterionResults())
	if err != nil {
		return nil, err
	}
	run, err := workRunProof(request.Msg.GetRunId(), request.Msg.GetRunAttempt(), request.Msg.GetRunFence())
	if err != nil {
		return nil, err
	}
	item, err := s.store.TransitionWork(ctx, workapp.TransitionCommand{RequestID: requestID, Actor: actor, Agent: identity.agent, Run: run, WorkID: workID, ToState: state, Reason: request.Msg.GetReason(), Result: request.Msg.GetResult(), CriterionResults: results, Now: now})
	if err != nil {
		return nil, serviceError(err)
	}
	message, err := workMessage(item)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&workv1.TransitionWorkResponse{Work: message}), nil
}

func (s *Service) RequestApproval(ctx context.Context, request *connect.Request[workv1.RequestApprovalRequest]) (*connect.Response[workv1.RequestApprovalResponse], error) {
	identity, now, err := s.mutationIdentity(ctx, request.Header())
	if err != nil {
		return nil, err
	}
	actor := identity.human
	if !actor.Valid() {
		actor = identity.agent.Principal
	}
	requestID, workID, _, err := mutationIDs(request.Msg.GetRequestId(), request.Msg.GetWorkId(), "work id", "", "")
	if err != nil {
		return nil, err
	}
	run, err := workRunProof(request.Msg.GetRunId(), request.Msg.GetRunAttempt(), request.Msg.GetRunFence())
	if err != nil {
		return nil, err
	}
	approval, err := s.store.RequestWorkApproval(ctx, workapp.RequestApprovalCommand{RequestID: requestID, Actor: actor, Agent: identity.agent, Run: run, WorkID: workID, Question: request.Msg.GetQuestion(), Now: now})
	if err != nil {
		return nil, serviceError(err)
	}
	message, err := approvalMessage(approval)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&workv1.RequestApprovalResponse{Approval: message}), nil
}

func (s *Service) ResolveApproval(ctx context.Context, request *connect.Request[workv1.ResolveApprovalRequest]) (*connect.Response[workv1.ResolveApprovalResponse], error) {
	actor, now, err := s.mutationActor(ctx, request.Header())
	if err != nil {
		return nil, err
	}
	requestID, approvalID, _, err := mutationIDs(request.Msg.GetRequestId(), request.Msg.GetApprovalId(), "approval id", "", "")
	if err != nil {
		return nil, err
	}
	decision, err := approvalDecisionValue(request.Msg.GetDecision())
	if err != nil {
		return nil, err
	}
	approval, err := s.store.ResolveWorkApproval(ctx, workapp.ResolveApprovalCommand{RequestID: requestID, Actor: actor, ApprovalID: approvalID, Decision: decision, Note: request.Msg.GetNote(), Now: now})
	if err != nil {
		return nil, serviceError(err)
	}
	message, err := approvalMessage(approval)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&workv1.ResolveApprovalResponse{Approval: message}), nil
}

type identity struct {
	human authoritydomain.Principal
	agent authorityapp.RuntimeAuthentication
}

func (s *Service) resolve(ctx context.Context, header http.Header, mutation bool) (identity, time.Time, error) {
	now := s.now()
	resolved, err := sharedauthentication.Resolve(ctx, s.store, header, mutation, s.origin, now)
	if err != nil {
		return identity{}, time.Time{}, authenticationError(err)
	}
	if human, ok := resolved.Human(); ok {
		return identity{human: human}, now, nil
	}
	if agent, ok := resolved.Agent(); ok {
		return identity{agent: agent}, now, nil
	}
	return identity{}, time.Time{}, internalError()
}

func (s *Service) mutationActor(ctx context.Context, header http.Header) (authoritydomain.Principal, time.Time, error) {
	resolved, now, err := s.mutationIdentity(ctx, header)
	if err != nil {
		return authoritydomain.Principal{}, time.Time{}, err
	}
	if resolved.human.Valid() {
		return resolved.human, now, nil
	}
	if resolved.agent.Principal.Valid() {
		return resolved.agent.Principal, now, nil
	}
	return authoritydomain.Principal{}, time.Time{}, internalError()
}

func (s *Service) mutationIdentity(ctx context.Context, header http.Header) (identity, time.Time, error) {
	resolved, now, err := s.resolve(ctx, header, true)
	if err != nil {
		return identity{}, time.Time{}, err
	}
	if resolved.human.Valid() || resolved.agent.Valid() {
		return resolved, now, nil
	}
	return identity{}, time.Time{}, internalError()
}

func workRunProof(runID string, attempt, fence uint64) (*authorityapp.RunProof, error) {
	if runID == "" && attempt == 0 && fence == 0 {
		return nil, nil
	}
	id, err := connectid.CanonicalID(runID, "run id")
	if err != nil || attempt == 0 || fence == 0 {
		return nil, invalidArgument()
	}
	return &authorityapp.RunProof{RunID: id, Attempt: attempt, Fence: fence}, nil
}

func createParams(request *workv1.CreateWorkRequest, actor authoritydomain.Principal, now time.Time) (workapp.CreateCommand, error) {
	requestID, err := connectid.CanonicalID(request.GetRequestId(), "request id")
	if err != nil {
		return workapp.CreateCommand{}, err
	}
	parentID := ""
	if request.GetParentWorkId() != "" {
		parentID, err = connectid.CanonicalID(request.GetParentWorkId(), "parent work id")
		if err != nil {
			return workapp.CreateCommand{}, err
		}
	}
	messageID, err := connectid.CanonicalID(request.GetSourceMessageId(), "source message id")
	if err != nil {
		return workapp.CreateCommand{}, err
	}
	spaceID, err := connectid.CanonicalID(request.GetSourceSpaceId(), "source space id")
	if err != nil {
		return workapp.CreateCommand{}, err
	}
	target, err := messageTarget(request.GetSourceTarget())
	if err != nil {
		return workapp.CreateCommand{}, err
	}
	return workapp.CreateCommand{RequestID: requestID, Actor: actor, ParentWorkID: parentID, SourceMessageID: messageID, SourceSpaceID: spaceID, SourceTarget: target, SourceTargetSequence: request.GetSourceTargetSequence(), Goal: request.GetGoal(), Constraints: request.GetConstraints(), AcceptanceCriteria: request.GetAcceptanceCriteria(), Now: now}, nil
}

func mutationIDs(requestIDValue, itemValue, itemName, extraValue, extraName string) (string, string, string, error) {
	requestID, err := connectid.CanonicalID(requestIDValue, "request id")
	if err != nil {
		return "", "", "", err
	}
	itemID, err := connectid.CanonicalID(itemValue, itemName)
	if err != nil {
		return "", "", "", err
	}
	if extraName == "" {
		return requestID, itemID, "", nil
	}
	extraID, err := connectid.CanonicalID(extraValue, extraName)
	if err != nil {
		return "", "", "", err
	}
	return requestID, itemID, extraID, nil
}

func messageTarget(value *spacev1.MessageTarget) (collaborationapp.MessageTarget, error) {
	if value == nil {
		return collaborationapp.MessageTarget{}, invalidArgument()
	}
	switch target := value.GetTarget().(type) {
	case *spacev1.MessageTarget_SpaceId:
		id, err := connectid.CanonicalID(target.SpaceId, "source target space id")
		if err != nil {
			return collaborationapp.MessageTarget{}, err
		}
		return collaborationapp.MessageTarget{Kind: collaborationdomain.TargetSpace, ID: id}, nil
	case *spacev1.MessageTarget_ThreadRootMessageId:
		id, err := connectid.CanonicalID(target.ThreadRootMessageId, "source target thread id")
		if err != nil {
			return collaborationapp.MessageTarget{}, err
		}
		return collaborationapp.MessageTarget{Kind: collaborationdomain.TargetThread, ID: id}, nil
	default:
		return collaborationapp.MessageTarget{}, invalidArgument()
	}
}

func criterionResults(values []*workv1.WorkCriterionResultInput) ([]workapp.CriterionResultInput, error) {
	result := make([]workapp.CriterionResultInput, 0, len(values))
	for _, value := range values {
		if value == nil {
			return nil, invalidArgument()
		}
		criterionID, err := connectid.CanonicalID(value.GetCriterionId(), "criterion id")
		if err != nil {
			return nil, err
		}
		verdict, err := criterionVerdictValue(value.GetVerdict())
		if err != nil {
			return nil, err
		}
		result = append(result, workapp.CriterionResultInput{CriterionID: criterionID, Verdict: verdict, Evidence: value.GetEvidence()})
	}
	return result, nil
}

func workMessage(value workapp.Work) (*workv1.Work, error) {
	state, err := workStateMessage(value.State)
	if err != nil {
		return nil, err
	}
	creator, err := principalMessage(value.Creator)
	if err != nil {
		return nil, err
	}
	target, err := messageTargetMessage(value.SourceTarget)
	if err != nil {
		return nil, err
	}
	message := &workv1.Work{Id: value.ID, OrganizationId: value.OrganizationID, RootWorkId: value.RootWorkID, ParentWorkId: value.ParentWorkID, SourceMessageId: value.SourceMessageID, SourceSpaceId: value.SourceSpaceID, SourceTarget: target, SourceTargetSequence: value.SourceTargetSequence, TeamSpaceId: value.TeamSpaceID, Goal: value.Goal, State: state, BlockingReason: value.BlockingReason, Result: value.Result, Creator: creator, CreatedAt: timestamppb.New(value.CreatedAt), UpdatedAt: timestamppb.New(value.UpdatedAt), StateChangedAt: timestamppb.New(value.StateChangedAt)}
	if value.CompletedAt != nil {
		message.CompletedAt = timestamppb.New(*value.CompletedAt)
	}
	if value.FailedAt != nil {
		message.FailedAt = timestamppb.New(*value.FailedAt)
	}
	if value.CancelledAt != nil {
		message.CancelledAt = timestamppb.New(*value.CancelledAt)
	}
	return message, nil
}

func detailMessage(value workapp.Detail) (*workv1.WorkDetail, error) {
	work, err := workMessage(value.Work)
	if err != nil {
		return nil, err
	}
	result := &workv1.WorkDetail{Work: work, Constraints: make([]*workv1.WorkText, 0, len(value.Constraints)), AcceptanceCriteria: make([]*workv1.WorkCriterion, 0, len(value.AcceptanceCriteria)), Assignments: make([]*workv1.WorkAssignment, 0, len(value.Assignments)), Approvals: make([]*workv1.WorkApproval, 0, len(value.Approvals)), CriterionResults: make([]*workv1.WorkCriterionResult, 0, len(value.CriterionResults)), Events: make([]*workv1.WorkEvent, 0, len(value.Events))}
	for _, item := range value.Constraints {
		result.Constraints = append(result.Constraints, &workv1.WorkText{Id: item.ID, Ordinal: item.Ordinal, Body: item.Body, CreatedAt: timestamppb.New(item.CreatedAt)})
	}
	for _, item := range value.AcceptanceCriteria {
		result.AcceptanceCriteria = append(result.AcceptanceCriteria, &workv1.WorkCriterion{Id: item.ID, Ordinal: item.Ordinal, Body: item.Body, CreatedAt: timestamppb.New(item.CreatedAt)})
	}
	for _, item := range value.Assignments {
		message, err := assignmentMessage(item)
		if err != nil {
			return nil, err
		}
		result.Assignments = append(result.Assignments, message)
	}
	for _, item := range value.Approvals {
		message, err := approvalMessage(item)
		if err != nil {
			return nil, err
		}
		result.Approvals = append(result.Approvals, message)
	}
	for _, item := range value.CriterionResults {
		message, err := criterionResultMessage(item)
		if err != nil {
			return nil, err
		}
		result.CriterionResults = append(result.CriterionResults, message)
	}
	for _, item := range value.Events {
		message, err := eventMessage(item)
		if err != nil {
			return nil, err
		}
		result.Events = append(result.Events, message)
	}
	return result, nil
}

func assignmentMessage(value workapp.Assignment) (*workv1.WorkAssignment, error) {
	role, err := assignmentRoleMessage(value.Role)
	if err != nil {
		return nil, err
	}
	actor, err := principalMessage(value.AssignedBy)
	if err != nil {
		return nil, err
	}
	message := &workv1.WorkAssignment{Id: value.ID, WorkId: value.WorkID, OrganizationId: value.OrganizationID, Role: role, AgentId: value.AgentID, HolderComputerId: value.HolderComputerID, HolderPlacementDesiredRevision: value.HolderPlacementDesiredRevision, AssignedBy: actor, AssignedAt: timestamppb.New(value.AssignedAt), EndReason: value.EndReason}
	if value.EndedAt != nil {
		message.EndedAt = timestamppb.New(*value.EndedAt)
	}
	return message, nil
}

func approvalMessage(value workapp.Approval) (*workv1.WorkApproval, error) {
	status, err := approvalStatusMessage(value.Status)
	if err != nil {
		return nil, err
	}
	actor, err := principalMessage(value.RequestedBy)
	if err != nil {
		return nil, err
	}
	message := &workv1.WorkApproval{Id: value.ID, WorkId: value.WorkID, OrganizationId: value.OrganizationID, Status: status, Question: value.Question, RequestedBy: actor, RequestedAt: timestamppb.New(value.RequestedAt), DecidedByHumanId: value.DecidedByHumanID, DecisionNote: value.DecisionNote}
	if value.DecidedAt != nil {
		message.DecidedAt = timestamppb.New(*value.DecidedAt)
	}
	return message, nil
}

func criterionResultMessage(value workapp.CriterionResult) (*workv1.WorkCriterionResult, error) {
	verdict, err := criterionVerdictMessage(value.Verdict)
	if err != nil {
		return nil, err
	}
	actor, err := principalMessage(value.Actor)
	if err != nil {
		return nil, err
	}
	return &workv1.WorkCriterionResult{Sequence: value.Sequence, Id: value.ID, WorkId: value.WorkID, OrganizationId: value.OrganizationID, CriterionId: value.CriterionID, Verdict: verdict, Evidence: value.Evidence, Actor: actor, OccurredAt: timestamppb.New(value.OccurredAt)}, nil
}

func eventMessage(value workapp.Event) (*workv1.WorkEvent, error) {
	actor, err := principalMessage(value.Actor)
	if err != nil {
		return nil, err
	}
	from, err := workStateMessage(value.FromState)
	if err != nil {
		return nil, err
	}
	to, err := workStateMessage(value.ToState)
	if err != nil {
		return nil, err
	}
	return &workv1.WorkEvent{Sequence: value.Sequence, Id: value.ID, WorkId: value.WorkID, OrganizationId: value.OrganizationID, Kind: value.Kind, Actor: actor, FromState: from, ToState: to, ReferenceKind: value.ReferenceKind, ReferenceId: value.ReferenceID, Reason: value.Reason, OccurredAt: timestamppb.New(value.OccurredAt)}, nil
}

func messageTargetMessage(value collaborationapp.MessageTarget) (*spacev1.MessageTarget, error) {
	if value.Kind == collaborationdomain.TargetSpace {
		return &spacev1.MessageTarget{Target: &spacev1.MessageTarget_SpaceId{SpaceId: value.ID}}, nil
	}
	if value.Kind == collaborationdomain.TargetThread {
		return &spacev1.MessageTarget{Target: &spacev1.MessageTarget_ThreadRootMessageId{ThreadRootMessageId: value.ID}}, nil
	}
	return nil, internalError()
}

func principalMessage(value authoritydomain.Principal) (*grantv1.Principal, error) {
	kind := grantv1.PrincipalKind_PRINCIPAL_KIND_UNSPECIFIED
	switch value.Kind {
	case "human":
		kind = grantv1.PrincipalKind_PRINCIPAL_KIND_HUMAN
	case "agent":
		kind = grantv1.PrincipalKind_PRINCIPAL_KIND_AGENT
	case "system":
		kind = grantv1.PrincipalKind_PRINCIPAL_KIND_SYSTEM
	default:
		return nil, internalError()
	}
	return &grantv1.Principal{Kind: kind, Id: value.ID}, nil
}

func workStateValue(value workv1.WorkState) (string, error) {
	switch value {
	case workv1.WorkState_WORK_STATE_OPEN:
		return workapp.StateOpen, nil
	case workv1.WorkState_WORK_STATE_BLOCKED:
		return workapp.StateBlocked, nil
	case workv1.WorkState_WORK_STATE_COMPLETED:
		return workapp.StateCompleted, nil
	case workv1.WorkState_WORK_STATE_FAILED:
		return workapp.StateFailed, nil
	case workv1.WorkState_WORK_STATE_CANCELLED:
		return workapp.StateCancelled, nil
	default:
		return "", invalidArgument()
	}
}
func workStateMessage(value string) (workv1.WorkState, error) {
	switch value {
	case "":
		return workv1.WorkState_WORK_STATE_UNSPECIFIED, nil
	case workapp.StateOpen:
		return workv1.WorkState_WORK_STATE_OPEN, nil
	case workapp.StateBlocked:
		return workv1.WorkState_WORK_STATE_BLOCKED, nil
	case workapp.StateWaitingApproval:
		return workv1.WorkState_WORK_STATE_WAITING_APPROVAL, nil
	case workapp.StateCompleted:
		return workv1.WorkState_WORK_STATE_COMPLETED, nil
	case workapp.StateFailed:
		return workv1.WorkState_WORK_STATE_FAILED, nil
	case workapp.StateCancelled:
		return workv1.WorkState_WORK_STATE_CANCELLED, nil
	default:
		return workv1.WorkState_WORK_STATE_UNSPECIFIED, internalError()
	}
}
func assignmentRoleValue(value workv1.WorkAssignmentRole) (string, error) {
	switch value {
	case workv1.WorkAssignmentRole_WORK_ASSIGNMENT_ROLE_COORDINATOR:
		return workapp.AssignmentCoordinator, nil
	case workv1.WorkAssignmentRole_WORK_ASSIGNMENT_ROLE_CONTRIBUTOR:
		return workapp.AssignmentContributor, nil
	default:
		return "", invalidArgument()
	}
}
func assignmentRoleMessage(value string) (workv1.WorkAssignmentRole, error) {
	switch value {
	case workapp.AssignmentCoordinator:
		return workv1.WorkAssignmentRole_WORK_ASSIGNMENT_ROLE_COORDINATOR, nil
	case workapp.AssignmentContributor:
		return workv1.WorkAssignmentRole_WORK_ASSIGNMENT_ROLE_CONTRIBUTOR, nil
	default:
		return workv1.WorkAssignmentRole_WORK_ASSIGNMENT_ROLE_UNSPECIFIED, internalError()
	}
}
func approvalDecisionValue(value workv1.WorkApprovalDecision) (string, error) {
	switch value {
	case workv1.WorkApprovalDecision_WORK_APPROVAL_DECISION_APPROVED:
		return "approved", nil
	case workv1.WorkApprovalDecision_WORK_APPROVAL_DECISION_REJECTED:
		return "rejected", nil
	default:
		return "", invalidArgument()
	}
}
func approvalStatusMessage(value string) (workv1.WorkApprovalStatus, error) {
	switch value {
	case "pending":
		return workv1.WorkApprovalStatus_WORK_APPROVAL_STATUS_PENDING, nil
	case "approved":
		return workv1.WorkApprovalStatus_WORK_APPROVAL_STATUS_APPROVED, nil
	case "rejected":
		return workv1.WorkApprovalStatus_WORK_APPROVAL_STATUS_REJECTED, nil
	case "cancelled":
		return workv1.WorkApprovalStatus_WORK_APPROVAL_STATUS_CANCELLED, nil
	default:
		return workv1.WorkApprovalStatus_WORK_APPROVAL_STATUS_UNSPECIFIED, internalError()
	}
}
func criterionVerdictValue(value workv1.WorkCriterionVerdict) (string, error) {
	switch value {
	case workv1.WorkCriterionVerdict_WORK_CRITERION_VERDICT_PASSED:
		return "passed", nil
	case workv1.WorkCriterionVerdict_WORK_CRITERION_VERDICT_FAILED:
		return "failed", nil
	default:
		return "", invalidArgument()
	}
}
func criterionVerdictMessage(value string) (workv1.WorkCriterionVerdict, error) {
	switch value {
	case "passed":
		return workv1.WorkCriterionVerdict_WORK_CRITERION_VERDICT_PASSED, nil
	case "failed":
		return workv1.WorkCriterionVerdict_WORK_CRITERION_VERDICT_FAILED, nil
	default:
		return workv1.WorkCriterionVerdict_WORK_CRITERION_VERDICT_UNSPECIFIED, internalError()
	}
}

func authenticationError(err error) error {
	if errors.Is(err, sharedauthentication.ErrUnauthenticated) {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("work authentication invalid"))
	}
	if errors.Is(err, sharedauthentication.ErrSameOrigin) {
		return connect.NewError(connect.CodePermissionDenied, errors.New("same-origin browser request required"))
	}
	return internalError()
}
func invalidArgument() error {
	return connect.NewError(connect.CodeInvalidArgument, errors.New("work input is invalid"))
}
func internalError() error {
	return connect.NewError(connect.CodeInternal, errors.New("work service unavailable"))
}
func serviceError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.Canceled):
		return connect.NewError(connect.CodeCanceled, errors.New("work request canceled"))
	case errors.Is(err, context.DeadlineExceeded):
		return connect.NewError(connect.CodeDeadlineExceeded, errors.New("work request deadline exceeded"))
	case errors.Is(err, workapp.ErrCursorUnavailable):
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("cursor unavailable"))
	case errors.Is(err, authorityapp.ErrRuntimeUnauthenticated):
		return connect.NewError(connect.CodeUnauthenticated, errors.New("work authentication invalid"))
	case errors.Is(err, workapp.ErrNotFound), errors.Is(err, workapp.ErrApprovalNotFound):
		return connect.NewError(connect.CodeNotFound, errors.New("work fact not found"))
	case errors.Is(err, authoritydomain.ErrPermissionDenied):
		return connect.NewError(connect.CodePermissionDenied, errors.New("work action denied"))
	case errors.Is(err, workapp.ErrRequestConflict):
		return connect.NewError(connect.CodeAlreadyExists, errors.New("work request conflicts with committed request"))
	case errors.Is(err, workapp.ErrTransitionInvalid), errors.Is(err, workapp.ErrTerminal), errors.Is(err, workapp.ErrAcceptanceIncomplete), errors.Is(err, workapp.ErrApprovalConflict), errors.Is(err, workapp.ErrAssignmentConflict), errors.Is(err, workapp.ErrPlacementInvalid):
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("work state conflict"))
	case errors.Is(err, executionapp.ErrRunNotFound), errors.Is(err, executionapp.ErrRunNotRunning), errors.Is(err, executionapp.ErrRunLeaseStale), errors.Is(err, executionapp.ErrRunLeaseExpired):
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("work Run proof is stale"))
	case errors.Is(err, workapp.ErrInvalid):
		return invalidArgument()
	default:
		return internalError()
	}
}
