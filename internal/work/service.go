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
	"github.com/abcdlsj/sumi/internal/servicesvc"
	"github.com/abcdlsj/sumi/internal/transport/connectid"
	workapp "github.com/abcdlsj/sumi/internal/work/application"
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

func New(db workStore, browserOrigin string) *Service {
	return &Service{store: db, origin: browserOrigin, now: time.Now}
}

type identity struct {
	human authoritydomain.Principal
	agent authorityapp.RuntimeAuthentication
}

// ── Handlers ─────────────────────────────────────────────────

func (s *Service) ListWorks(ctx context.Context, req *connect.Request[workv1.ListWorksRequest]) (*connect.Response[workv1.ListWorksResponse], error) {
	ident, now, err := s.resolve(ctx, req.Header(), false)
	if err != nil {
		return nil, err
	}
	page, err := s.store.ListWorkPage(ctx, workapp.ListQuery{
		Actor: ident.human, Agent: ident.agent,
		Cursor: req.Msg.GetCursor(), Limit: req.Msg.GetLimit(), Now: now,
	})
	if err != nil {
		return nil, serviceErr(err)
	}
	resp := &workv1.ListWorksResponse{
		Works:      make([]*workv1.Work, 0, len(page.Works)),
		NextCursor: page.NextCursor,
	}
	for _, w := range page.Works {
		msg, err := workToProto(w)
		if err != nil {
			return nil, err
		}
		resp.Works = append(resp.Works, msg)
	}
	return connect.NewResponse(resp), nil
}

func (s *Service) GetWork(ctx context.Context, req *connect.Request[workv1.GetWorkRequest]) (*connect.Response[workv1.GetWorkResponse], error) {
	ident, now, err := s.resolve(ctx, req.Header(), false)
	if err != nil {
		return nil, err
	}
	workID, err := connectid.CanonicalID(req.Msg.GetWorkId(), "work id")
	if err != nil {
		return nil, err
	}
	detail, err := s.store.GetWorkDetail(ctx, workapp.ReadQuery{
		Actor: ident.human, Agent: ident.agent, WorkID: workID, Now: now,
	})
	if err != nil {
		return nil, serviceErr(err)
	}
	msg, err := detailToProto(detail)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&workv1.GetWorkResponse{Detail: msg}), nil
}

func (s *Service) CreateWork(ctx context.Context, req *connect.Request[workv1.CreateWorkRequest]) (*connect.Response[workv1.CreateWorkResponse], error) {
	ident, now, err := s.mutationIdentity(ctx, req.Header())
	if err != nil {
		return nil, err
	}
	actor := pickActor(ident)
	params, err := buildCreateParams(req.Msg, actor, now)
	if err != nil {
		return nil, err
	}
	params.Agent = ident.agent
	params.Run, err = buildRunProof(req.Msg.GetRunProof())
	if err != nil {
		return nil, err
	}
	w, err := s.store.CreateWork(ctx, params)
	if err != nil {
		return nil, serviceErr(err)
	}
	msg, err := workToProto(w)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&workv1.CreateWorkResponse{Work: msg}), nil
}

func (s *Service) AssignWork(ctx context.Context, req *connect.Request[workv1.AssignWorkRequest]) (*connect.Response[workv1.AssignWorkResponse], error) {
	ident, now, err := s.mutationIdentity(ctx, req.Header())
	if err != nil {
		return nil, err
	}
	actor := pickActor(ident)
	requestID, workID, agentID, err := parseIDs(req.Msg.GetRequestId(), req.Msg.GetWorkId(), "work id", req.Msg.GetAgentId(), "agent id")
	if err != nil {
		return nil, err
	}
	role, err := servicesvc.RoleFromProto(req.Msg.GetRole())
	if err != nil {
		return nil, err
	}
	run, err := buildRunProof(req.Msg.GetRunProof())
	if err != nil {
		return nil, err
	}
	assignment, err := s.store.AssignWork(ctx, workapp.AssignCommand{
		RequestID: requestID, Actor: actor, Agent: ident.agent, Run: run,
		WorkID: workID, Role: role, AgentID: agentID, Now: now,
	})
	if err != nil {
		return nil, serviceErr(err)
	}
	msg, err := assignmentToProto(assignment)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&workv1.AssignWorkResponse{Assignment: msg}), nil
}

func (s *Service) TransitionWork(ctx context.Context, req *connect.Request[workv1.TransitionWorkRequest]) (*connect.Response[workv1.TransitionWorkResponse], error) {
	ident, now, err := s.mutationIdentity(ctx, req.Header())
	if err != nil {
		return nil, err
	}
	actor := pickActor(ident)
	requestID, workID, _, err := parseIDs(req.Msg.GetRequestId(), req.Msg.GetWorkId(), "work id", "", "")
	if err != nil {
		return nil, err
	}
	state, err := servicesvc.StateFromProto(req.Msg.GetToState())
	if err != nil {
		return nil, err
	}
	results, err := buildCriterionResults(req.Msg.GetCriterionResults())
	if err != nil {
		return nil, err
	}
	run, err := buildRunProof(req.Msg.GetRunProof())
	if err != nil {
		return nil, err
	}
	w, err := s.store.TransitionWork(ctx, workapp.TransitionCommand{
		RequestID: requestID, Actor: actor, Agent: ident.agent, Run: run,
		WorkID: workID, ToState: state, Reason: req.Msg.GetReason(),
		Result: req.Msg.GetResult(), CriterionResults: results, Now: now,
	})
	if err != nil {
		return nil, serviceErr(err)
	}
	msg, err := workToProto(w)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&workv1.TransitionWorkResponse{Work: msg}), nil
}

func (s *Service) RequestApproval(ctx context.Context, req *connect.Request[workv1.RequestApprovalRequest]) (*connect.Response[workv1.RequestApprovalResponse], error) {
	ident, now, err := s.mutationIdentity(ctx, req.Header())
	if err != nil {
		return nil, err
	}
	actor := pickActor(ident)
	requestID, workID, _, err := parseIDs(req.Msg.GetRequestId(), req.Msg.GetWorkId(), "work id", "", "")
	if err != nil {
		return nil, err
	}
	run, err := buildRunProof(req.Msg.GetRunProof())
	if err != nil {
		return nil, err
	}
	approval, err := s.store.RequestWorkApproval(ctx, workapp.RequestApprovalCommand{
		RequestID: requestID, Actor: actor, Agent: ident.agent, Run: run,
		WorkID: workID, Question: req.Msg.GetQuestion(), Now: now,
	})
	if err != nil {
		return nil, serviceErr(err)
	}
	msg, err := approvalToProto(approval)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&workv1.RequestApprovalResponse{Approval: msg}), nil
}

func (s *Service) ResolveApproval(ctx context.Context, req *connect.Request[workv1.ResolveApprovalRequest]) (*connect.Response[workv1.ResolveApprovalResponse], error) {
	actor, now, err := s.mutationActor(ctx, req.Header())
	if err != nil {
		return nil, err
	}
	requestID, approvalID, _, err := parseIDs(req.Msg.GetRequestId(), req.Msg.GetApprovalId(), "approval id", "", "")
	if err != nil {
		return nil, err
	}
	decision, err := servicesvc.DecisionFromProto(req.Msg.GetDecision())
	if err != nil {
		return nil, err
	}
	approval, err := s.store.ResolveWorkApproval(ctx, workapp.ResolveApprovalCommand{
		RequestID: requestID, Actor: actor,
		ApprovalID: approvalID, Decision: decision,
		Note: req.Msg.GetNote(), Now: now,
	})
	if err != nil {
		return nil, serviceErr(err)
	}
	msg, err := approvalToProto(approval)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&workv1.ResolveApprovalResponse{Approval: msg}), nil
}

// ── Identity ─────────────────────────────────────────────────

func (s *Service) resolve(ctx context.Context, header http.Header, mutation bool) (identity, time.Time, error) {
	now := s.now()
	resolved, err := sharedauthentication.Resolve(ctx, s.store, header, mutation, s.origin, now)
	if err != nil {
		return identity{}, time.Time{}, authErr(err)
	}
	if h, ok := resolved.Human(); ok {
		return identity{human: h}, now, nil
	}
	if a, ok := resolved.Agent(); ok {
		return identity{agent: a}, now, nil
	}
	return identity{}, time.Time{}, servicesvc.ErrInternal
}

func (s *Service) mutationActor(ctx context.Context, header http.Header) (authoritydomain.Principal, time.Time, error) {
	ident, now, err := s.mutationIdentity(ctx, header)
	if err != nil {
		return authoritydomain.Principal{}, time.Time{}, err
	}
	return pickActor(ident), now, nil
}

func (s *Service) mutationIdentity(ctx context.Context, header http.Header) (identity, time.Time, error) {
	ident, now, err := s.resolve(ctx, header, true)
	if err != nil {
		return identity{}, time.Time{}, err
	}
	if ident.human.Valid() || ident.agent.Valid() {
		return ident, now, nil
	}
	return identity{}, time.Time{}, servicesvc.ErrInternal
}

func pickActor(ident identity) authoritydomain.Principal {
	if ident.human.Valid() {
		return ident.human
	}
	return ident.agent.Principal
}

// ── Params ───────────────────────────────────────────────────

func buildCreateParams(msg *workv1.CreateWorkRequest, actor authoritydomain.Principal, now time.Time) (workapp.CreateCommand, error) {
	requestID, err := connectid.CanonicalID(msg.GetRequestId(), "request id")
	if err != nil {
		return workapp.CreateCommand{}, err
	}
	parentID := ""
	if msg.GetParentWorkId() != "" {
		parentID, err = connectid.CanonicalID(msg.GetParentWorkId(), "parent work id")
		if err != nil {
			return workapp.CreateCommand{}, err
		}
	}
	messageID, err := connectid.CanonicalID(msg.GetSourceMessageId(), "source message id")
	if err != nil {
		return workapp.CreateCommand{}, err
	}
	spaceID, err := connectid.CanonicalID(msg.GetSourceSpaceId(), "source space id")
	if err != nil {
		return workapp.CreateCommand{}, err
	}
	target, err := parseMsgTarget(msg.GetSourceTarget())
	if err != nil {
		return workapp.CreateCommand{}, err
	}
	return workapp.CreateCommand{
		RequestID: requestID, Actor: actor, ParentWorkID: parentID,
		SourceMessageID: messageID, SourceSpaceID: spaceID, SourceTarget: target,
		SourceTargetSequence: msg.GetSourceTargetSequence(),
		Goal: msg.GetGoal(), Constraints: msg.GetConstraints(),
		AcceptanceCriteria: msg.GetAcceptanceCriteria(), Now: now,
	}, nil
}

func buildRunProof(p *grantv1.RunProof) (*authorityapp.RunProof, error) {
	if p == nil {
		return nil, nil
	}
	id, err := connectid.CanonicalID(p.GetRunId(), "run id")
	if err != nil || p.GetAttempt() == 0 || p.GetFence() == 0 {
		return nil, servicesvc.InvalArg("work input is invalid")
	}
	return &authorityapp.RunProof{RunID: id, Attempt: p.GetAttempt(), Fence: p.GetFence()}, nil
}

func parseIDs(requestIDValue, itemValue, itemName, extraValue, extraName string) (string, string, string, error) {
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

func parseMsgTarget(v *spacev1.MessageTarget) (collaborationapp.MessageTarget, error) {
	if v == nil {
		return collaborationapp.MessageTarget{}, servicesvc.InvalArg("work input is invalid")
	}
	switch t := v.GetTarget().(type) {
	case *spacev1.MessageTarget_SpaceId:
		id, err := connectid.CanonicalID(t.SpaceId, "source target space id")
		if err != nil {
			return collaborationapp.MessageTarget{}, err
		}
		return collaborationapp.MessageTarget{Kind: collaborationdomain.TargetSpace, ID: id}, nil
	case *spacev1.MessageTarget_ThreadRootMessageId:
		id, err := connectid.CanonicalID(t.ThreadRootMessageId, "source target thread id")
		if err != nil {
			return collaborationapp.MessageTarget{}, err
		}
		return collaborationapp.MessageTarget{Kind: collaborationdomain.TargetThread, ID: id}, nil
	default:
		return collaborationapp.MessageTarget{}, servicesvc.InvalArg("work input is invalid")
	}
}

func buildCriterionResults(values []*workv1.WorkCriterionResultInput) ([]workapp.CriterionResultInput, error) {
	result := make([]workapp.CriterionResultInput, 0, len(values))
	for _, v := range values {
		if v == nil {
			return nil, servicesvc.InvalArg("work input is invalid")
		}
		criterionID, err := connectid.CanonicalID(v.GetCriterionId(), "criterion id")
		if err != nil {
			return nil, err
		}
		verdict, err := servicesvc.VerdictFromProto(v.GetVerdict())
		if err != nil {
			return nil, err
		}
		result = append(result, workapp.CriterionResultInput{
			CriterionID: criterionID, Verdict: verdict, Evidence: v.GetEvidence(),
		})
	}
	return result, nil
}

// ── Proto converters ─────────────────────────────────────────

func workToProto(w workapp.Work) (*workv1.Work, error) {
	state, err := servicesvc.StateToProto(w.State)
	if err != nil {
		return nil, err
	}
	creator, err := servicesvc.ToPrincipal(w.Creator)
	if err != nil {
		return nil, err
	}
	target, err := servicesvc.MsgTargetToProto(w.SourceTarget)
	if err != nil {
		return nil, err
	}
	msg := &workv1.Work{
		Id: w.ID, OrganizationId: w.OrganizationID, RootWorkId: w.RootWorkID,
		ParentWorkId: w.ParentWorkID, SourceMessageId: w.SourceMessageID,
		SourceSpaceId: w.SourceSpaceID, SourceTarget: target,
		SourceTargetSequence: w.SourceTargetSequence, TeamSpaceId: w.TeamSpaceID,
		Goal: w.Goal, State: state, BlockingReason: w.BlockingReason,
		Result: w.Result, Creator: creator,
		CreatedAt: servicesvc.Ts(w.CreatedAt), UpdatedAt: servicesvc.Ts(w.UpdatedAt),
		StateChangedAt: servicesvc.Ts(w.StateChangedAt),
	}
	if w.CompletedAt != nil {
		msg.CompletedAt = servicesvc.Ts(*w.CompletedAt)
	}
	if w.FailedAt != nil {
		msg.FailedAt = servicesvc.Ts(*w.FailedAt)
	}
	if w.CancelledAt != nil {
		msg.CancelledAt = servicesvc.Ts(*w.CancelledAt)
	}
	return msg, nil
}

func detailToProto(d workapp.Detail) (*workv1.WorkDetail, error) {
	w, err := workToProto(d.Work)
	if err != nil {
		return nil, err
	}
	result := &workv1.WorkDetail{
		Work: w,
		Constraints:         make([]*workv1.WorkText, 0, len(d.Constraints)),
		AcceptanceCriteria:  make([]*workv1.WorkCriterion, 0, len(d.AcceptanceCriteria)),
		Assignments:         make([]*workv1.WorkAssignment, 0, len(d.Assignments)),
		Approvals:           make([]*workv1.WorkApproval, 0, len(d.Approvals)),
		CriterionResults:    make([]*workv1.WorkCriterionResult, 0, len(d.CriterionResults)),
		Events:              make([]*workv1.WorkEvent, 0, len(d.Events)),
	}
	for _, c := range d.Constraints {
		result.Constraints = append(result.Constraints, &workv1.WorkText{
			Id: c.ID, Ordinal: c.Ordinal, Body: c.Body, CreatedAt: servicesvc.Ts(c.CreatedAt),
		})
	}
	for _, c := range d.AcceptanceCriteria {
		result.AcceptanceCriteria = append(result.AcceptanceCriteria, &workv1.WorkCriterion{
			Id: c.ID, Ordinal: c.Ordinal, Body: c.Body, CreatedAt: servicesvc.Ts(c.CreatedAt),
		})
	}
	for _, a := range d.Assignments {
		msg, err := assignmentToProto(a)
		if err != nil {
			return nil, err
		}
		result.Assignments = append(result.Assignments, msg)
	}
	for _, a := range d.Approvals {
		msg, err := approvalToProto(a)
		if err != nil {
			return nil, err
		}
		result.Approvals = append(result.Approvals, msg)
	}
	for _, cr := range d.CriterionResults {
		msg, err := criterionResultToProto(cr)
		if err != nil {
			return nil, err
		}
		result.CriterionResults = append(result.CriterionResults, msg)
	}
	for _, e := range d.Events {
		msg, err := eventToProto(e)
		if err != nil {
			return nil, err
		}
		result.Events = append(result.Events, msg)
	}
	return result, nil
}

func assignmentToProto(a workapp.Assignment) (*workv1.WorkAssignment, error) {
	role, err := servicesvc.RoleToProto(a.Role)
	if err != nil {
		return nil, err
	}
	actor, err := servicesvc.ToPrincipal(a.AssignedBy)
	if err != nil {
		return nil, err
	}
	msg := &workv1.WorkAssignment{
		Id: a.ID, WorkId: a.WorkID, OrganizationId: a.OrganizationID,
		Role: role, AgentId: a.AgentID, HolderComputerId: a.HolderComputerID,
		HolderPlacementDesiredRevision: a.HolderPlacementDesiredRevision,
		AssignedBy: actor, AssignedAt: servicesvc.Ts(a.AssignedAt),
		EndReason: a.EndReason,
	}
	if a.EndedAt != nil {
		msg.EndedAt = servicesvc.Ts(*a.EndedAt)
	}
	return msg, nil
}

func approvalToProto(a workapp.Approval) (*workv1.WorkApproval, error) {
	status, err := servicesvc.StatusToProto(a.Status)
	if err != nil {
		return nil, err
	}
	actor, err := servicesvc.ToPrincipal(a.RequestedBy)
	if err != nil {
		return nil, err
	}
	msg := &workv1.WorkApproval{
		Id: a.ID, WorkId: a.WorkID, OrganizationId: a.OrganizationID,
		Status: status, Question: a.Question, RequestedBy: actor,
		RequestedAt: servicesvc.Ts(a.RequestedAt),
		DecidedByHumanId: a.DecidedByHumanID, DecisionNote: a.DecisionNote,
	}
	if a.DecidedAt != nil {
		msg.DecidedAt = servicesvc.Ts(*a.DecidedAt)
	}
	return msg, nil
}

func criterionResultToProto(cr workapp.CriterionResult) (*workv1.WorkCriterionResult, error) {
	verdict, err := servicesvc.VerdictToProto(cr.Verdict)
	if err != nil {
		return nil, err
	}
	actor, err := servicesvc.ToPrincipal(cr.Actor)
	if err != nil {
		return nil, err
	}
	return &workv1.WorkCriterionResult{
		Sequence: cr.Sequence, Id: cr.ID, WorkId: cr.WorkID,
		OrganizationId: cr.OrganizationID, CriterionId: cr.CriterionID,
		Verdict: verdict, Evidence: cr.Evidence, Actor: actor,
		OccurredAt: servicesvc.Ts(cr.OccurredAt),
	}, nil
}

func eventToProto(e workapp.Event) (*workv1.WorkEvent, error) {
	actor, err := servicesvc.ToPrincipal(e.Actor)
	if err != nil {
		return nil, err
	}
	from, err := servicesvc.StateToProto(e.FromState)
	if err != nil {
		return nil, err
	}
	to, err := servicesvc.StateToProto(e.ToState)
	if err != nil {
		return nil, err
	}
	return &workv1.WorkEvent{
		Sequence: e.Sequence, Id: e.ID, WorkId: e.WorkID,
		OrganizationId: e.OrganizationID, Kind: e.Kind, Actor: actor,
		FromState: from, ToState: to, ReferenceKind: e.ReferenceKind,
		ReferenceId: e.ReferenceID, Reason: e.Reason,
		OccurredAt: servicesvc.Ts(e.OccurredAt),
	}, nil
}

// ── Error helpers ────────────────────────────────────────────

func authErr(err error) error {
	switch {
	case errors.Is(err, sharedauthentication.ErrUnauthenticated):
		return connect.NewError(connect.CodeUnauthenticated, errors.New("work authentication invalid"))
	case errors.Is(err, sharedauthentication.ErrSameOrigin):
		return connect.NewError(connect.CodePermissionDenied, errors.New("same-origin browser request required"))
	default:
		return servicesvc.ErrInternal
	}
}

func serviceErr(err error) error {
	return servicesvc.ServiceErr(err)
}
