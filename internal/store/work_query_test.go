package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestGetWorkDetailRestoresCurrentFactsAfterRestart(t *testing.T) {
	f := openWorkQueryFixture(t)
	work := f.createWork(t, "detail recovery", f.now.Add(time.Second))
	first, second := f.createPlacedAgents(t, 2)
	firstAssignment, err := f.database.AssignWork(context.Background(), AssignWorkParams{RequestID: uuid.NewString(), Actor: f.owner, WorkID: work.ID, Role: WorkAssignmentCoordinator, AgentID: first, Now: f.now.Add(2 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	secondAssignment, err := f.database.AssignWork(context.Background(), AssignWorkParams{RequestID: uuid.NewString(), Actor: f.owner, WorkID: work.ID, Role: WorkAssignmentCoordinator, AgentID: second, Now: f.now.Add(3 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	criterion := work.AcceptanceCriteria[0]
	if _, err := f.database.TransitionWork(context.Background(), TransitionWorkParams{
		RequestID: uuid.NewString(), Actor: f.owner, WorkID: work.ID, ToState: WorkStateBlocked, Reason: "needs approval",
		CriterionResults: []WorkCriterionResultInput{{CriterionID: criterion.ID, Verdict: "passed", Evidence: "evidence"}}, Now: f.now.Add(4 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	approval, err := f.database.RequestWorkApproval(context.Background(), RequestWorkApprovalParams{RequestID: uuid.NewString(), Actor: f.owner, WorkID: work.ID, Question: "continue?", Now: f.now.Add(5 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.database.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := Open(f.path)
	if err != nil {
		t.Fatal(err)
	}
	f.database = restarted
	detail, err := restarted.GetWorkDetail(context.Background(), WorkReadParams{Actor: f.owner, WorkID: work.ID, Now: f.now.Add(6 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if detail.State != WorkStateWaitingApproval || len(detail.Constraints) != 1 || len(detail.AcceptanceCriteria) != 1 || len(detail.Assignments) != 2 || len(detail.Approvals) != 1 || len(detail.CriterionResults) != 1 || len(detail.Events) != 8 {
		t.Fatalf("restarted detail is incomplete: %+v", detail)
	}
	if detail.Assignments[0].EndedAt == nil || detail.Assignments[1].EndedAt != nil || detail.Approvals[0].Status != "pending" || detail.CriterionResults[0].Evidence != "evidence" {
		t.Fatalf("restarted detail lost current/history facts: %+v", detail)
	}
	expectedEvents := []struct {
		kind, referenceKind, referenceID, from, to string
	}{
		{kind: "created"},
		{kind: "assignment.started", referenceKind: "assignment", referenceID: firstAssignment.ID},
		{kind: "assignment.ended", referenceKind: "assignment", referenceID: firstAssignment.ID},
		{kind: "assignment.started", referenceKind: "assignment", referenceID: secondAssignment.ID},
		{kind: "acceptance.recorded", referenceKind: "criterion_result", referenceID: detail.CriterionResults[0].ID},
		{kind: "state.transitioned", from: WorkStateOpen, to: WorkStateBlocked},
		{kind: "approval.requested", referenceKind: "approval", referenceID: approval.ID},
		{kind: "state.transitioned", referenceKind: "approval", referenceID: approval.ID, from: WorkStateBlocked, to: WorkStateWaitingApproval},
	}
	for index, want := range expectedEvents {
		got := detail.Events[index]
		if got.Sequence != uint64(index+1) || got.Kind != want.kind || got.ReferenceKind != want.referenceKind || got.ReferenceID != want.referenceID || got.FromState != want.from || got.ToState != want.to {
			t.Fatalf("event %d = %+v, want %+v", index, got, want)
		}
	}
}

func TestGetWorkDetailFailsClosedForEveryCollectionIterationError(t *testing.T) {
	f := openWorkQueryFixture(t)
	work := f.createWork(t, "iteration", f.now.Add(time.Second))
	agent, _ := f.createPlacedAgents(t, 1)
	if _, err := f.database.AssignWork(context.Background(), AssignWorkParams{RequestID: uuid.NewString(), Actor: f.owner, WorkID: work.ID, Role: WorkAssignmentContributor, AgentID: agent, Now: f.now.Add(2 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.RequestWorkApproval(context.Background(), RequestWorkApprovalParams{RequestID: uuid.NewString(), Actor: f.owner, WorkID: work.ID, Question: "approve?", Now: f.now.Add(3 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{workRowsConstraints, workRowsCriteria, workRowsAssignments, workRowsApprovals, workRowsResults, workRowsEvents} {
		t.Run(source, func(t *testing.T) {
			ctx := context.WithValue(context.Background(), workRowsErrorContextKey{}, workRowsErrorFunc(func(got string, _ *sql.Rows) error {
				if got == source {
					return fmt.Errorf("forced %s iterator error", source)
				}
				return nil
			}))
			detail, err := f.database.GetWorkDetail(ctx, WorkReadParams{Actor: f.owner, WorkID: work.ID, Now: f.now.Add(4 * time.Second)})
			if err == nil || detail.Work.ID != "" || !stringsContain(err.Error(), "forced "+source+" iterator error") {
				t.Fatalf("%s detail/error = %+v, %v", source, detail, err)
			}
		})
	}
}

func TestListWorkPageIsBoundedStableAndDoesNotExposeHiddenWorks(t *testing.T) {
	f := openWorkQueryFixture(t)
	works := make([]Work, 0, workPageCandidateScan+1)
	for index := 0; index < workPageCandidateScan+1; index++ {
		works = append(works, f.createWork(t, fmt.Sprintf("page-%03d", index), f.now.Add(time.Duration(index+1)*time.Nanosecond)))
	}
	ownerPage := WorkPage{}
	cursor := ""
	seen := map[string]bool{}
	for {
		page, err := f.database.ListWorkPage(context.Background(), ListWorkPageParams{Actor: f.owner, Cursor: cursor, Limit: 200, Now: f.now.Add(time.Minute)})
		if err != nil {
			t.Fatal(err)
		}
		for _, work := range page.Works {
			if seen[work.ID] {
				t.Fatalf("duplicate work %s", work.ID)
			}
			seen[work.ID] = true
		}
		ownerPage = page
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(seen) != len(works) || ownerPage.NextCursor != "" {
		t.Fatalf("stable owner pages = %d/%d, last=%+v", len(seen), len(works), ownerPage)
	}

	ordered := make([]string, 0, len(seen))
	cursor = ""
	for {
		page, err := f.database.ListWorkPage(context.Background(), ListWorkPageParams{Actor: f.owner, Cursor: cursor, Limit: 200, Now: f.now.Add(2 * time.Minute)})
		if err != nil {
			t.Fatal(err)
		}
		for _, work := range page.Works {
			ordered = append(ordered, work.ID)
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	member := f.createMember(t, "page-member")
	targetID := ordered[workPageCandidateScan]
	if _, err := f.database.IssueGrant(context.Background(), IssueGrantParams{RequestID: uuid.NewString(), Actor: f.owner, Subject: member, Capability: CapabilityWorkRead, Scope: Scope{Kind: "work", ID: targetID}, ParentGrantID: f.rootGrant.ID, Now: f.now.Add(3 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	first, err := f.database.ListWorkPage(context.Background(), ListWorkPageParams{Actor: member, Limit: 200, Now: f.now.Add(4 * time.Minute)})
	if err != nil || len(first.Works) != 0 || first.NextCursor == "" {
		t.Fatalf("hidden first window = %+v, %v", first, err)
	}
	second, err := f.database.ListWorkPage(context.Background(), ListWorkPageParams{Actor: member, Cursor: first.NextCursor, Limit: 200, Now: f.now.Add(4*time.Minute + time.Nanosecond)})
	if err != nil || len(second.Works) != 1 || second.Works[0].ID != targetID || second.NextCursor != "" {
		t.Fatalf("continued member window = %+v, %v", second, err)
	}
	if stringsContain(first.NextCursor, targetID) || stringsContain(first.NextCursor, ordered[0]) {
		t.Fatal("sealed continuation cursor leaked work id")
	}

	exact400 := openWorkQueryFixture(t)
	for index := 0; index < workPageCandidateScan; index++ {
		exact400.createWork(t, fmt.Sprintf("exact-400-%03d", index), exact400.now.Add(time.Duration(index+1)*time.Nanosecond))
	}
	hiddenMember := exact400.createMember(t, "exact-400-hidden")
	page, err := exact400.database.ListWorkPage(context.Background(), ListWorkPageParams{Actor: hiddenMember, Limit: 200, Now: exact400.now.Add(time.Minute)})
	if err != nil || len(page.Works) != 0 || page.NextCursor != "" {
		t.Fatalf("exact 400 hidden page = %+v, %v", page, err)
	}
}

func TestListWorkPageFollowsFullHierarchyTuple(t *testing.T) {
	f := openWorkQueryFixture(t)
	root := f.createWork(t, "tuple-root", f.now.Add(time.Second))
	parentA := f.createChildWork(t, root.ID, "tuple-parent-a", f.now.Add(2*time.Second))
	parentB := f.createChildWork(t, root.ID, "tuple-parent-b", f.now.Add(2*time.Second))
	childAEarly := f.createChildWork(t, parentA.ID, "tuple-child-a-early", f.now.Add(3*time.Second))
	sameCreatedAt := f.now.Add(4 * time.Second)
	childAFirst := f.createChildWork(t, parentA.ID, "tuple-child-a-first", sameCreatedAt)
	childASecond := f.createChildWork(t, parentA.ID, "tuple-child-a-second", sameCreatedAt)
	childALater := f.createChildWork(t, parentA.ID, "tuple-child-a-later", f.now.Add(5*time.Second))
	childB := f.createChildWork(t, parentB.ID, "tuple-child-b", sameCreatedAt)
	otherRoot := f.createWork(t, "tuple-other-root", sameCreatedAt)
	works := []Work{root, parentA, parentB, childAEarly, childAFirst, childASecond, childALater, childB, otherRoot}
	sort.Slice(works, func(left, right int) bool {
		if works[left].RootWorkID != works[right].RootWorkID {
			return works[left].RootWorkID < works[right].RootWorkID
		}
		leftRoot, rightRoot := works[left].ParentWorkID == "", works[right].ParentWorkID == ""
		if leftRoot != rightRoot {
			return leftRoot
		}
		if works[left].ParentWorkID != works[right].ParentWorkID {
			return works[left].ParentWorkID < works[right].ParentWorkID
		}
		if works[left].CreatedAt != works[right].CreatedAt {
			return works[left].CreatedAt.Before(works[right].CreatedAt)
		}
		return works[left].ID < works[right].ID
	})
	for _, limit := range []uint32{1, 2} {
		t.Run(fmt.Sprintf("limit-%d", limit), func(t *testing.T) {
			cursor := ""
			got := make([]string, 0, len(works))
			seen := map[string]bool{}
			checkedRootCursor := false
			for {
				page, err := f.database.ListWorkPage(context.Background(), ListWorkPageParams{Actor: f.owner, Cursor: cursor, Limit: limit, Now: f.now.Add(3 * time.Second)})
				if err != nil {
					t.Fatal(err)
				}
				for _, work := range page.Works {
					if seen[work.ID] {
						t.Fatalf("duplicate work %s", work.ID)
					}
					seen[work.ID] = true
					got = append(got, work.ID)
				}
				if len(page.Works) > 0 && page.Works[len(page.Works)-1].ParentWorkID == "" && page.NextCursor != "" {
					seek, err := f.database.OpenWorkCursor(page.NextCursor, WorkCursorBinding{PrincipalFingerprint: workCursorPrincipalFingerprint(f.owner), OrganizationID: f.owner.OrganizationID})
					if err != nil || !seek.ParentIsNull {
						t.Fatalf("root continuation seek = %+v, %v", seek, err)
					}
					checkedRootCursor = true
				}
				if page.NextCursor == "" {
					break
				}
				cursor = page.NextCursor
			}
			if (limit == 1 && !checkedRootCursor) || len(got) != len(works) {
				t.Fatalf("hierarchy pages = %v, root cursor=%t", got, checkedRootCursor)
			}
			for index, work := range works {
				if got[index] != work.ID {
					t.Fatalf("work %d = %s, want %s", index, got[index], work.ID)
				}
			}
		})
	}
}

func TestListWorkPageFailsClosedOnCandidateIterationError(t *testing.T) {
	f := openWorkQueryFixture(t)
	f.createWork(t, "page iterator", f.now.Add(time.Second))
	ctx := context.WithValue(context.Background(), workRowsErrorContextKey{}, workRowsErrorFunc(func(source string, _ *sql.Rows) error {
		if source == workRowsPage {
			return errors.New("forced page iterator error")
		}
		return nil
	}))
	page, err := f.database.ListWorkPage(ctx, ListWorkPageParams{Actor: f.owner, Now: f.now.Add(2 * time.Second)})
	if err == nil || page.NextCursor != "" || len(page.Works) != 0 || !stringsContain(err.Error(), "forced page iterator error") {
		t.Fatalf("page/error = %+v, %v", page, err)
	}
}

func TestWorkReadCurrentPrincipalAndRuntimeAreRechecked(t *testing.T) {
	f := openWorkQueryFixture(t)
	work := f.createWork(t, "authorization", f.now.Add(time.Second))
	member := f.createMember(t, "disabled-reader")
	readGrant, err := f.database.IssueGrant(context.Background(), IssueGrantParams{RequestID: uuid.NewString(), Actor: f.owner, Subject: member, Capability: CapabilityWorkRead, Scope: Scope{Kind: "work", ID: work.ID}, ParentGrantID: f.rootGrant.ID, Now: f.now.Add(2 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.GetWorkDetail(context.Background(), WorkReadParams{Actor: member, WorkID: work.ID, Now: f.now.Add(3 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.RevokeGrant(context.Background(), RevokeGrantParams{RequestID: uuid.NewString(), Actor: f.owner, GrantID: readGrant.ID, Now: f.now.Add(4 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.GetWorkDetail(context.Background(), WorkReadParams{Actor: member, WorkID: work.ID, Now: f.now.Add(5 * time.Second)}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("revoked reader error = %v", err)
	}
	if _, err := f.database.IssueGrant(context.Background(), IssueGrantParams{RequestID: uuid.NewString(), Actor: f.owner, Subject: member, Capability: CapabilityWorkRead, Scope: Scope{Kind: "work", ID: work.ID}, ParentGrantID: f.rootGrant.ID, Now: f.now.Add(6 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.SetHumanStatus(context.Background(), SetHumanStatusParams{RequestID: uuid.NewString(), Actor: f.owner, HumanID: member.ID, Status: "disabled", Now: f.now.Add(7 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.GetWorkDetail(context.Background(), WorkReadParams{Actor: member, WorkID: work.ID, Now: f.now.Add(8 * time.Second)}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("disabled reader error = %v", err)
	}

	runtimeFixture := openAgentRuntimeFixture(t)
	bootstrap, err := runtimeFixture.database.EnsureAuthority(context.Background(), rtToken(250), runtimeFixture.now)
	if err != nil {
		t.Fatal(err)
	}
	owner := Principal{Kind: "human", ID: bootstrap.Human.ID, OrganizationID: bootstrap.Organization.ID}
	space, err := runtimeFixture.database.CreateGroup(context.Background(), CreateGroupParams{RequestID: uuid.NewString(), Actor: owner, Name: "runtime work", Now: runtimeFixture.now})
	if err != nil {
		t.Fatal(err)
	}
	message, err := runtimeFixture.database.SendMessage(context.Background(), SendMessageParams{RequestID: uuid.NewString(), Actor: owner, Target: MessageTarget{Kind: MessageTargetSpace, ID: space.ID}, Body: "runtime source", Now: runtimeFixture.now.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	runtimeWork, err := runtimeFixture.database.CreateWork(context.Background(), WorkCreateParams{RequestID: uuid.NewString(), Actor: owner, SourceMessageID: message.ID, SourceSpaceID: space.ID, SourceTarget: message.Target, SourceTargetSequence: message.TargetSequence, Goal: "runtime", AcceptanceCriteria: []string{"done"}, Now: runtimeFixture.now.Add(2 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	agent := Principal{Kind: "agent", ID: runtimeFixture.agentID, OrganizationID: owner.OrganizationID}
	if _, err := runtimeFixture.database.IssueGrant(context.Background(), IssueGrantParams{RequestID: uuid.NewString(), Actor: owner, Subject: agent, Capability: CapabilityWorkRead, Scope: Scope{Kind: "work", ID: runtimeWork.ID}, ParentGrantID: bootstrap.RootGrant.ID, Now: runtimeFixture.now.Add(3 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	token := rtToken(177)
	createRuntimeSession(t, runtimeFixture, token, runtimeFixture.now.Add(4*time.Second))
	authentication, err := runtimeFixture.database.AuthenticateAgentRuntimeSession(context.Background(), token, runtimeFixture.now.Add(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	readers := []struct {
		name string
		read func(WorkReadParams) error
	}{
		{name: "detail", read: func(params WorkReadParams) error {
			_, err := runtimeFixture.database.GetWorkDetail(context.Background(), params)
			return err
		}},
		{name: "page", read: func(params WorkReadParams) error {
			_, err := runtimeFixture.database.ListWorkPage(context.Background(), ListWorkPageParams{Actor: params.Actor, Agent: params.Agent, Now: params.Now})
			return err
		}},
	}
	now := runtimeFixture.now.Add(5 * time.Second)
	for _, reader := range readers {
		t.Run(reader.name+" valid human", func(t *testing.T) {
			if err := reader.read(WorkReadParams{Actor: owner, WorkID: runtimeWork.ID, Now: now}); err != nil {
				t.Fatal(err)
			}
		})
		t.Run(reader.name+" valid runtime", func(t *testing.T) {
			if err := reader.read(WorkReadParams{Agent: authentication, WorkID: runtimeWork.ID, Now: now}); err != nil {
				t.Fatal(err)
			}
		})
	}
	partialWithoutKind := authentication
	partialWithoutKind.Principal.Kind = ""
	partialWithoutID := authentication
	partialWithoutID.Principal.ID = ""
	partialWithoutOrganization := authentication
	partialWithoutOrganization.Principal.OrganizationID = ""
	wrongOrganization := authentication
	wrongOrganization.Principal.OrganizationID = uuid.NewString()
	invalid := []struct {
		name   string
		params WorkReadParams
		want   error
	}{
		{name: "direct agent", params: WorkReadParams{Actor: agent, WorkID: runtimeWork.ID, Now: now}, want: ErrPermissionDenied},
		{name: "system actor", params: WorkReadParams{Actor: Principal{Kind: "system", OrganizationID: owner.OrganizationID}, WorkID: runtimeWork.ID, Now: now}, want: ErrPermissionDenied},
		{name: "unknown actor", params: WorkReadParams{Actor: Principal{Kind: "unknown", ID: uuid.NewString(), OrganizationID: owner.OrganizationID}, WorkID: runtimeWork.ID, Now: now}, want: ErrPermissionDenied},
		{name: "missing human", params: WorkReadParams{Actor: Principal{Kind: "human", ID: uuid.NewString(), OrganizationID: owner.OrganizationID}, WorkID: runtimeWork.ID, Now: now}, want: ErrPermissionDenied},
		{name: "partial human missing kind", params: WorkReadParams{Actor: Principal{ID: owner.ID, OrganizationID: owner.OrganizationID}, WorkID: runtimeWork.ID, Now: now}, want: ErrPermissionDenied},
		{name: "partial human missing id", params: WorkReadParams{Actor: Principal{Kind: "human", OrganizationID: owner.OrganizationID}, WorkID: runtimeWork.ID, Now: now}, want: ErrPermissionDenied},
		{name: "partial human missing organization", params: WorkReadParams{Actor: Principal{Kind: "human", ID: owner.ID}, WorkID: runtimeWork.ID, Now: now}, want: ErrPermissionDenied},
		{name: "partial runtime missing kind", params: WorkReadParams{Agent: partialWithoutKind, WorkID: runtimeWork.ID, Now: now}, want: ErrAgentRuntimeUnauthenticated},
		{name: "partial runtime missing id", params: WorkReadParams{Agent: partialWithoutID, WorkID: runtimeWork.ID, Now: now}, want: ErrAgentRuntimeUnauthenticated},
		{name: "partial runtime missing organization", params: WorkReadParams{Agent: partialWithoutOrganization, WorkID: runtimeWork.ID, Now: now}, want: ErrAgentRuntimeUnauthenticated},
		{name: "runtime wrong organization", params: WorkReadParams{Agent: wrongOrganization, WorkID: runtimeWork.ID, Now: now}, want: ErrAgentRuntimeUnauthenticated},
		{name: "human plus runtime", params: WorkReadParams{Actor: owner, Agent: authentication, WorkID: runtimeWork.ID, Now: now}, want: ErrAgentRuntimeUnauthenticated},
		{name: "agent plus same runtime", params: WorkReadParams{Actor: agent, Agent: authentication, WorkID: runtimeWork.ID, Now: now}, want: ErrAgentRuntimeUnauthenticated},
	}
	for _, reader := range readers {
		for _, test := range invalid {
			t.Run(reader.name+" "+test.name, func(t *testing.T) {
				if err := reader.read(test.params); !errors.Is(err, test.want) {
					t.Fatalf("error = %v, want %v", err, test.want)
				}
			})
		}
		expired := WorkReadParams{Agent: authentication, WorkID: runtimeWork.ID, Now: runtimeFixture.now.Add(14 * time.Minute)}
		t.Run(reader.name+" expired runtime", func(t *testing.T) {
			if err := reader.read(expired); !errors.Is(err, ErrAgentRuntimeUnauthenticated) {
				t.Fatalf("error = %v", err)
			}
		})
	}
	replacementToken := rtToken(178)
	createRuntimeSession(t, runtimeFixture, replacementToken, runtimeFixture.now.Add(6*time.Second))
	for _, reader := range readers {
		t.Run(reader.name+" stale runtime", func(t *testing.T) {
			if err := reader.read(WorkReadParams{Agent: authentication, WorkID: runtimeWork.ID, Now: runtimeFixture.now.Add(7 * time.Second)}); !errors.Is(err, ErrAgentRuntimeUnauthenticated) {
				t.Fatalf("error = %v", err)
			}
		})
	}
	replacement, err := runtimeFixture.database.AuthenticateAgentRuntimeSession(context.Background(), replacementToken, runtimeFixture.now.Add(7*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := runtimeFixture.database.RevokeAgentRuntimeSession(context.Background(), RevokeAgentRuntimeSessionParams{Proof: replacement.Proof, ComputerID: runtimeFixture.computer.ID, RegistrationKey: runtimeFixture.registrationKey, Now: runtimeFixture.now.Add(8 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	for _, reader := range readers {
		t.Run(reader.name+" revoked runtime", func(t *testing.T) {
			if err := reader.read(WorkReadParams{Agent: replacement, WorkID: runtimeWork.ID, Now: runtimeFixture.now.Add(9 * time.Second)}); !errors.Is(err, ErrAgentRuntimeUnauthenticated) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

type workQueryFixture struct {
	path      string
	database  *Store
	now       time.Time
	owner     Principal
	rootGrant Grant
	space     Space
	message   Message
}

func openWorkQueryFixture(t *testing.T) *workQueryFixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "server.db")
	database, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	f := &workQueryFixture{path: path, database: database, now: time.Date(2026, time.July, 23, 9, 0, 0, 0, time.UTC)}
	t.Cleanup(func() {
		if f.database != nil {
			_ = f.database.Close()
		}
	})
	bootstrap, err := database.EnsureAuthority(context.Background(), "work-query-bootstrap-credential-abcdefghijklmnopqrstuvwxyz", f.now)
	if err != nil {
		t.Fatal(err)
	}
	f.owner = Principal{Kind: "human", ID: bootstrap.Human.ID, OrganizationID: bootstrap.Organization.ID}
	f.rootGrant = bootstrap.RootGrant
	f.space, err = database.CreateGroup(context.Background(), CreateGroupParams{RequestID: uuid.NewString(), Actor: f.owner, Name: "work query source", Now: f.now})
	if err != nil {
		t.Fatal(err)
	}
	f.message, err = database.SendMessage(context.Background(), SendMessageParams{RequestID: uuid.NewString(), Actor: f.owner, Target: MessageTarget{Kind: MessageTargetSpace, ID: f.space.ID}, Body: "work query source", Now: f.now.Add(time.Nanosecond)})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *workQueryFixture) createWork(t *testing.T, goal string, now time.Time) Work {
	return f.createChildWork(t, "", goal, now)
}

func (f *workQueryFixture) createChildWork(t *testing.T, parentWorkID, goal string, now time.Time) Work {
	t.Helper()
	work, err := f.database.CreateWork(context.Background(), WorkCreateParams{RequestID: uuid.NewString(), Actor: f.owner, ParentWorkID: parentWorkID, SourceMessageID: f.message.ID, SourceSpaceID: f.space.ID, SourceTarget: f.message.Target, SourceTargetSequence: f.message.TargetSequence, Goal: goal, Constraints: []string{"constraint"}, AcceptanceCriteria: []string{"criterion"}, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	return work
}

func (f *workQueryFixture) createPlacedAgents(t *testing.T, count int) (string, string) {
	t.Helper()
	ids := make([]string, count)
	for index := range ids {
		agent, err := f.database.CreateAgent(context.Background(), testCreateAgentParams(f.owner, fmt.Sprintf("work-query-agent-%d-%s", index, uuid.NewString()[:8]), f.now.Add(time.Duration(index+1)*time.Second)))
		if err != nil {
			t.Fatal(err)
		}
		configureTestRuntimeSpec(t, f.database, f.owner, agent.ID, f.now.Add(time.Duration(index+1)*time.Second))
		computer := pairTestComputer(t, f.database, f.owner, "work-query-computer-"+uuid.NewString(), testCapabilityInventory("test", true), f.now)
		computerID := computer.ID
		if _, err := f.database.db.Exec(`INSERT INTO agent_placements(agent_id, computer_id, agent_profile_revision, runtime_spec_revision, desired_revision, state, error_code, created_at, updated_at) VALUES(?, ?, 1, 1, 1, 'ready', '', ?, ?)`, agent.ID, computerID, unixNano(f.now), unixNano(f.now)); err != nil {
			t.Fatal(err)
		}
		ids[index] = agent.ID
	}
	if count == 1 {
		return ids[0], ""
	}
	return ids[0], ids[1]
}

func (f *workQueryFixture) createMember(t *testing.T, name string) Principal {
	t.Helper()
	human, err := f.database.CreateHuman(context.Background(), CreateHumanParams{RequestID: uuid.NewString(), Actor: f.owner, Name: name + "-" + uuid.NewString()[:8], Role: "member", Credential: name + "-credential-abcdefghijklmnopqrstuvwxyz", Now: f.now.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	return Principal{Kind: "human", ID: human.ID, OrganizationID: f.owner.OrganizationID}
}

func stringsContain(value, want string) bool {
	return len(want) > 0 && len(value) >= len(want) && contains(value, want)
}

func contains(value, want string) bool {
	for index := 0; index+len(want) <= len(value); index++ {
		if value[index:index+len(want)] == want {
			return true
		}
	}
	return false
}
