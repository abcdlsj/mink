package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestGetWorkDetailRestoresCurrentFactsAfterRestart(t *testing.T) {
	fixture := openWorkQueryFixture(t)
	work := fixture.createWork(t, "detail recovery", fixture.now.Add(time.Second))
	first, second := fixture.createPlacedAgents(t, 2)
	if _, err := fixture.database.AssignWork(context.Background(), AssignWorkParams{RequestID: uuid.NewString(), Actor: fixture.owner, WorkID: work.ID, Role: WorkAssignmentCoordinator, AgentID: first, Now: fixture.now.Add(2 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.AssignWork(context.Background(), AssignWorkParams{RequestID: uuid.NewString(), Actor: fixture.owner, WorkID: work.ID, Role: WorkAssignmentCoordinator, AgentID: second, Now: fixture.now.Add(3 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	criterion := work.AcceptanceCriteria[0]
	if _, err := fixture.database.TransitionWork(context.Background(), TransitionWorkParams{
		RequestID: uuid.NewString(), Actor: fixture.owner, WorkID: work.ID, ToState: WorkStateBlocked, Reason: "needs approval",
		CriterionResults: []WorkCriterionResultInput{{CriterionID: criterion.ID, Verdict: "passed", Evidence: "evidence"}}, Now: fixture.now.Add(4 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.RequestWorkApproval(context.Background(), RequestWorkApprovalParams{RequestID: uuid.NewString(), Actor: fixture.owner, WorkID: work.ID, Question: "continue?", Now: fixture.now.Add(5 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := Open(fixture.path)
	if err != nil {
		t.Fatal(err)
	}
	fixture.database = restarted
	detail, err := restarted.GetWorkDetail(context.Background(), WorkReadParams{Actor: fixture.owner, WorkID: work.ID, Now: fixture.now.Add(6 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if detail.State != WorkStateWaitingApproval || len(detail.Constraints) != 1 || len(detail.AcceptanceCriteria) != 1 || len(detail.Assignments) != 2 || len(detail.Approvals) != 1 || len(detail.CriterionResults) != 1 || len(detail.Events) < 7 {
		t.Fatalf("restarted detail is incomplete: %+v", detail)
	}
	if detail.Assignments[0].EndedAt == nil || detail.Assignments[1].EndedAt != nil || detail.Approvals[0].Status != "pending" || detail.CriterionResults[0].Evidence != "evidence" {
		t.Fatalf("restarted detail lost current/history facts: %+v", detail)
	}
}

func TestGetWorkDetailFailsClosedForEveryCollectionIterationError(t *testing.T) {
	fixture := openWorkQueryFixture(t)
	work := fixture.createWork(t, "iteration", fixture.now.Add(time.Second))
	agent, _ := fixture.createPlacedAgents(t, 1)
	if _, err := fixture.database.AssignWork(context.Background(), AssignWorkParams{RequestID: uuid.NewString(), Actor: fixture.owner, WorkID: work.ID, Role: WorkAssignmentContributor, AgentID: agent, Now: fixture.now.Add(2 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.RequestWorkApproval(context.Background(), RequestWorkApprovalParams{RequestID: uuid.NewString(), Actor: fixture.owner, WorkID: work.ID, Question: "approve?", Now: fixture.now.Add(3 * time.Second)}); err != nil {
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
			detail, err := fixture.database.GetWorkDetail(ctx, WorkReadParams{Actor: fixture.owner, WorkID: work.ID, Now: fixture.now.Add(4 * time.Second)})
			if err == nil || detail.Work.ID != "" || !stringsContain(err.Error(), "forced "+source+" iterator error") {
				t.Fatalf("%s detail/error = %+v, %v", source, detail, err)
			}
		})
	}
}

func TestListWorkPageIsBoundedStableAndDoesNotExposeHiddenWorks(t *testing.T) {
	fixture := openWorkQueryFixture(t)
	works := make([]Work, 0, workPageCandidateScan+1)
	for index := 0; index < workPageCandidateScan+1; index++ {
		works = append(works, fixture.createWork(t, fmt.Sprintf("page-%03d", index), fixture.now.Add(time.Duration(index+1)*time.Nanosecond)))
	}
	ownerPage := WorkPage{}
	cursor := ""
	seen := map[string]bool{}
	for {
		page, err := fixture.database.ListWorkPage(context.Background(), ListWorkPageParams{Actor: fixture.owner, Cursor: cursor, Limit: 200, Now: fixture.now.Add(time.Minute)})
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
		page, err := fixture.database.ListWorkPage(context.Background(), ListWorkPageParams{Actor: fixture.owner, Cursor: cursor, Limit: 200, Now: fixture.now.Add(2 * time.Minute)})
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
	member := fixture.createMember(t, "page-member")
	targetID := ordered[workPageCandidateScan]
	if _, err := fixture.database.IssueGrant(context.Background(), IssueGrantParams{RequestID: uuid.NewString(), Actor: fixture.owner, Subject: member, Capability: CapabilityWorkRead, Scope: Scope{Kind: "work", ID: targetID}, ParentGrantID: fixture.rootGrant.ID, Now: fixture.now.Add(3 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	first, err := fixture.database.ListWorkPage(context.Background(), ListWorkPageParams{Actor: member, Limit: 200, Now: fixture.now.Add(4 * time.Minute)})
	if err != nil || len(first.Works) != 0 || first.NextCursor == "" {
		t.Fatalf("hidden first window = %+v, %v", first, err)
	}
	second, err := fixture.database.ListWorkPage(context.Background(), ListWorkPageParams{Actor: member, Cursor: first.NextCursor, Limit: 200, Now: fixture.now.Add(4*time.Minute + time.Nanosecond)})
	if err != nil || len(second.Works) != 1 || second.Works[0].ID != targetID || second.NextCursor != "" {
		t.Fatalf("continued member window = %+v, %v", second, err)
	}
	if stringsContain(first.NextCursor, targetID) || stringsContain(first.NextCursor, ordered[0]) {
		t.Fatal("sealed continuation cursor leaked work id")
	}
}

func TestListWorkPageFailsClosedOnCandidateIterationError(t *testing.T) {
	fixture := openWorkQueryFixture(t)
	fixture.createWork(t, "page iterator", fixture.now.Add(time.Second))
	ctx := context.WithValue(context.Background(), workRowsErrorContextKey{}, workRowsErrorFunc(func(source string, _ *sql.Rows) error {
		if source == workRowsPage {
			return errors.New("forced page iterator error")
		}
		return nil
	}))
	page, err := fixture.database.ListWorkPage(ctx, ListWorkPageParams{Actor: fixture.owner, Now: fixture.now.Add(2 * time.Second)})
	if err == nil || page.NextCursor != "" || len(page.Works) != 0 || !stringsContain(err.Error(), "forced page iterator error") {
		t.Fatalf("page/error = %+v, %v", page, err)
	}
}

func TestWorkReadCurrentPrincipalAndRuntimeAreRechecked(t *testing.T) {
	fixture := openWorkQueryFixture(t)
	work := fixture.createWork(t, "authorization", fixture.now.Add(time.Second))
	member := fixture.createMember(t, "disabled-reader")
	readGrant, err := fixture.database.IssueGrant(context.Background(), IssueGrantParams{RequestID: uuid.NewString(), Actor: fixture.owner, Subject: member, Capability: CapabilityWorkRead, Scope: Scope{Kind: "work", ID: work.ID}, ParentGrantID: fixture.rootGrant.ID, Now: fixture.now.Add(2 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.GetWorkDetail(context.Background(), WorkReadParams{Actor: member, WorkID: work.ID, Now: fixture.now.Add(3 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.RevokeGrant(context.Background(), RevokeGrantParams{RequestID: uuid.NewString(), Actor: fixture.owner, GrantID: readGrant.ID, Now: fixture.now.Add(4 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.GetWorkDetail(context.Background(), WorkReadParams{Actor: member, WorkID: work.ID, Now: fixture.now.Add(5 * time.Second)}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("revoked reader error = %v", err)
	}
	if _, err := fixture.database.IssueGrant(context.Background(), IssueGrantParams{RequestID: uuid.NewString(), Actor: fixture.owner, Subject: member, Capability: CapabilityWorkRead, Scope: Scope{Kind: "work", ID: work.ID}, ParentGrantID: fixture.rootGrant.ID, Now: fixture.now.Add(6 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.SetHumanStatus(context.Background(), SetHumanStatusParams{RequestID: uuid.NewString(), Actor: fixture.owner, HumanID: member.ID, Status: "disabled", Now: fixture.now.Add(7 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.GetWorkDetail(context.Background(), WorkReadParams{Actor: member, WorkID: work.ID, Now: fixture.now.Add(8 * time.Second)}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("disabled reader error = %v", err)
	}

	runtimeFixture := openAgentRuntimeFixture(t)
	bootstrap, err := runtimeFixture.database.EnsureAuthority(context.Background(), runtimeTestToken(250), runtimeFixture.now)
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
	token := runtimeTestToken(177)
	createRuntimeSession(t, runtimeFixture, token, runtimeFixture.now.Add(4*time.Second))
	authentication, err := runtimeFixture.database.AuthenticateAgentRuntimeSession(context.Background(), token, runtimeFixture.now.Add(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtimeFixture.database.GetWorkDetail(context.Background(), WorkReadParams{Agent: authentication, WorkID: runtimeWork.ID, Now: runtimeFixture.now.Add(5 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	if err := runtimeFixture.database.RevokeAgentRuntimeSession(context.Background(), RevokeAgentRuntimeSessionParams{Proof: authentication.Proof, ComputerID: runtimeFixture.computer.ID, RegistrationKey: runtimeFixture.registrationKey, Now: runtimeFixture.now.Add(6 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtimeFixture.database.GetWorkDetail(context.Background(), WorkReadParams{Agent: authentication, WorkID: runtimeWork.ID, Now: runtimeFixture.now.Add(7 * time.Second)}); !errors.Is(err, ErrAgentRuntimeUnauthenticated) {
		t.Fatalf("stale runtime error = %v", err)
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
	fixture := &workQueryFixture{path: path, database: database, now: time.Date(2026, time.July, 23, 9, 0, 0, 0, time.UTC)}
	t.Cleanup(func() {
		if fixture.database != nil {
			_ = fixture.database.Close()
		}
	})
	bootstrap, err := database.EnsureAuthority(context.Background(), "work-query-bootstrap-credential-abcdefghijklmnopqrstuvwxyz", fixture.now)
	if err != nil {
		t.Fatal(err)
	}
	fixture.owner = Principal{Kind: "human", ID: bootstrap.Human.ID, OrganizationID: bootstrap.Organization.ID}
	fixture.rootGrant = bootstrap.RootGrant
	fixture.space, err = database.CreateGroup(context.Background(), CreateGroupParams{RequestID: uuid.NewString(), Actor: fixture.owner, Name: "work query source", Now: fixture.now})
	if err != nil {
		t.Fatal(err)
	}
	fixture.message, err = database.SendMessage(context.Background(), SendMessageParams{RequestID: uuid.NewString(), Actor: fixture.owner, Target: MessageTarget{Kind: MessageTargetSpace, ID: fixture.space.ID}, Body: "work query source", Now: fixture.now.Add(time.Nanosecond)})
	if err != nil {
		t.Fatal(err)
	}
	return fixture
}

func (fixture *workQueryFixture) createWork(t *testing.T, goal string, now time.Time) Work {
	t.Helper()
	work, err := fixture.database.CreateWork(context.Background(), WorkCreateParams{RequestID: uuid.NewString(), Actor: fixture.owner, SourceMessageID: fixture.message.ID, SourceSpaceID: fixture.space.ID, SourceTarget: fixture.message.Target, SourceTargetSequence: fixture.message.TargetSequence, Goal: goal, Constraints: []string{"constraint"}, AcceptanceCriteria: []string{"criterion"}, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	return work
}

func (fixture *workQueryFixture) createPlacedAgents(t *testing.T, count int) (string, string) {
	t.Helper()
	ids := make([]string, count)
	for index := range ids {
		agent, err := fixture.database.CreateAgent(context.Background(), CreateAgentParams{RequestID: uuid.NewString(), Actor: fixture.owner, Name: fmt.Sprintf("work-query-agent-%d-%s", index, uuid.NewString()[:8]), Description: "work query agent", Driver: "native", Now: fixture.now.Add(time.Duration(index+1) * time.Second)})
		if err != nil {
			t.Fatal(err)
		}
		computerID := uuid.NewString()
		if _, err := fixture.database.db.Exec(`INSERT INTO computers(id, registration_key_hash, name, os, arch, created_at, last_seen_at) VALUES(?, randomblob(32), ?, 'linux', 'amd64', ?, ?)`, computerID, "work-query-computer", unixNano(fixture.now), unixNano(fixture.now)); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.database.db.Exec(`INSERT INTO agent_placements(agent_id, computer_id, generation, state, error_code, created_at, updated_at) VALUES(?, ?, 1, 'active', '', ?, ?)`, agent.ID, computerID, unixNano(fixture.now), unixNano(fixture.now)); err != nil {
			t.Fatal(err)
		}
		ids[index] = agent.ID
	}
	if count == 1 {
		return ids[0], ""
	}
	return ids[0], ids[1]
}

func (fixture *workQueryFixture) createMember(t *testing.T, name string) Principal {
	t.Helper()
	human, err := fixture.database.CreateHuman(context.Background(), CreateHumanParams{RequestID: uuid.NewString(), Actor: fixture.owner, Name: name + "-" + uuid.NewString()[:8], Role: "member", Credential: name + "-credential-abcdefghijklmnopqrstuvwxyz", Now: fixture.now.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	return Principal{Kind: "human", ID: human.ID, OrganizationID: fixture.owner.OrganizationID}
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
