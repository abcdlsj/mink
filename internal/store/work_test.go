package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestWorkFactsCreateTreeAssignApprovalTransitionAndReplay(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 7, 21, 14, 0, 0, 0, time.UTC)
	bootstrap, err := database.EnsureAuthority(context.Background(), "work-facts-bootstrap-credential-abcdefghijklmnopqrstuvwxyz", now)
	if err != nil {
		t.Fatal(err)
	}
	owner := Principal{Kind: "human", ID: bootstrap.Human.ID, OrganizationID: bootstrap.Organization.ID}
	space, err := database.CreateGroup(context.Background(), CreateGroupParams{RequestID: uuid.NewString(), Actor: owner, Name: "source", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	source, err := database.SendMessage(context.Background(), SendMessageParams{RequestID: uuid.NewString(), Actor: owner, Target: MessageTarget{Kind: MessageTargetSpace, ID: space.ID}, Body: "delegate this", Now: now.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	member := createTestHuman(t, database, owner, "work-denied-member", "member", "work-denied-member-credential-abcdefghijklmnopqrstuvwxyz", now.Add(2*time.Second))
	memberPrincipal := Principal{Kind: "human", ID: member.ID, OrganizationID: owner.OrganizationID}
	var worksBefore, spacesBefore, receiptsBefore int
	if err := database.db.QueryRow(`SELECT count(*) FROM works`).Scan(&worksBefore); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRow(`SELECT count(*) FROM spaces`).Scan(&spacesBefore); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRow(`SELECT count(*) FROM work_requests`).Scan(&receiptsBefore); err != nil {
		t.Fatal(err)
	}
	deniedCreate := WorkCreateParams{RequestID: uuid.NewString(), Actor: memberPrincipal, SourceMessageID: source.ID, SourceSpaceID: space.ID, SourceTarget: source.Target, SourceTargetSequence: source.TargetSequence, Goal: "denied", AcceptanceCriteria: []string{"never"}, Now: now.Add(3 * time.Second)}
	if _, err := database.CreateWork(context.Background(), deniedCreate); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("denied work create = %v", err)
	}
	var worksAfter, spacesAfter, receiptsAfter int
	if err := database.db.QueryRow(`SELECT count(*) FROM works`).Scan(&worksAfter); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRow(`SELECT count(*) FROM spaces`).Scan(&spacesAfter); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRow(`SELECT count(*) FROM work_requests`).Scan(&receiptsAfter); err != nil {
		t.Fatal(err)
	}
	if worksAfter != worksBefore || spacesAfter != spacesBefore || receiptsAfter != receiptsBefore {
		t.Fatalf("denied work create mutated facts: works %d/%d spaces %d/%d receipts %d/%d", worksBefore, worksAfter, spacesBefore, spacesAfter, receiptsBefore, receiptsAfter)
	}
	agent, err := database.CreateAgent(context.Background(), CreateAgentParams{RequestID: uuid.NewString(), Actor: owner, Name: "worker", Description: "worker", Driver: "native", Now: now.Add(2 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	computerID := uuid.NewString()
	if _, err := database.db.Exec(`INSERT INTO computers(id, registration_key_hash, name, os, arch, created_at, last_seen_at) VALUES(?, zeroblob(32), 'computer', 'linux', 'amd64', ?, ?)`, computerID, unixNano(now), unixNano(now)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.Exec(`INSERT INTO agent_placements(agent_id, computer_id, generation, state, error_code, created_at, updated_at) VALUES(?, ?, 1, 'active', '', ?, ?)`, agent.ID, computerID, unixNano(now), unixNano(now)); err != nil {
		t.Fatal(err)
	}
	create := WorkCreateParams{RequestID: uuid.NewString(), Actor: owner, SourceMessageID: source.ID, SourceSpaceID: space.ID, SourceTarget: source.Target, SourceTargetSequence: source.TargetSequence, Goal: "investigate", Constraints: []string{"no destructive changes"}, AcceptanceCriteria: []string{"written result"}, Now: now.Add(3 * time.Second)}
	work, err := database.CreateWork(context.Background(), create)
	if err != nil {
		t.Fatal(err)
	}
	if work.State != WorkStateOpen || work.RootWorkID != work.ID || work.TeamSpaceID == "" || len(work.AcceptanceCriteria) != 1 {
		t.Fatalf("created work = %+v", work)
	}
	if _, err := database.IssueGrant(context.Background(), IssueGrantParams{RequestID: uuid.NewString(), Actor: owner, Subject: Principal{Kind: "agent", ID: agent.ID}, Capability: CapabilityWorkCreate, Scope: Scope{Kind: "work", ID: work.ID}, ParentGrantID: bootstrap.RootGrant.ID, Now: now.Add(3 * time.Second)}); !errors.Is(err, ErrGrantInvalid) {
		t.Fatalf("work.create work scope = %v", err)
	}
	workGrant, err := database.IssueGrant(context.Background(), IssueGrantParams{RequestID: uuid.NewString(), Actor: owner, Subject: Principal{Kind: "agent", ID: agent.ID}, Capability: CapabilityWorkManage, Scope: Scope{Kind: "work", ID: work.ID}, ParentGrantID: bootstrap.RootGrant.ID, Now: now.Add(3 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	allowed, err := database.CheckPermission(context.Background(), CheckPermissionParams{
		Subject:    Principal{Kind: "agent", ID: agent.ID, OrganizationID: bootstrap.Organization.ID},
		Capability: CapabilityWorkManage, Scope: Scope{Kind: "work", ID: work.ID}, Now: now.Add(3 * time.Second),
	})
	if err != nil || !allowed || workGrant.Scope.ID != work.ID {
		t.Fatalf("work grant permission = %v, %v", allowed, err)
	}
	replayed, err := database.CreateWork(context.Background(), create)
	if err != nil || replayed.ID != work.ID {
		t.Fatalf("create replay = %+v, %v", replayed, err)
	}
	create.Goal = "different"
	if _, err := database.CreateWork(context.Background(), create); !errors.Is(err, ErrWorkRequestConflict) {
		t.Fatalf("create conflict = %v", err)
	}
	assignment, err := database.AssignWork(context.Background(), AssignWorkParams{RequestID: uuid.NewString(), Actor: owner, WorkID: work.ID, Role: WorkAssignmentCoordinator, AgentID: agent.ID, Now: now.Add(4 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if assignment.HolderComputerID != computerID || assignment.HolderPlacementGeneration != 1 {
		t.Fatalf("assignment = %+v", assignment)
	}
	firstAssignment := assignment
	assignment, err = database.AssignWork(context.Background(), AssignWorkParams{RequestID: uuid.NewString(), Actor: owner, WorkID: work.ID, Role: WorkAssignmentCoordinator, AgentID: agent.ID, Now: now.Add(4 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	var reassignedAt int64
	if err := database.db.QueryRow(`SELECT ended_at FROM work_assignments WHERE id = ?`, firstAssignment.ID).Scan(&reassignedAt); err != nil {
		t.Fatal(err)
	}
	if reassignedAt == 0 {
		t.Fatal("reassigned work did not end previous assignment")
	}
	var reassignedEvents int
	if err := database.db.QueryRow(`SELECT count(*) FROM work_events WHERE work_id = ? AND event_kind = 'assignment.ended' AND reference_id = ?`, work.ID, firstAssignment.ID).Scan(&reassignedEvents); err != nil {
		t.Fatal(err)
	}
	if reassignedEvents != 1 {
		t.Fatalf("reassigned assignment ended events = %d", reassignedEvents)
	}
	approval, err := database.RequestWorkApproval(context.Background(), RequestWorkApprovalParams{RequestID: uuid.NewString(), Actor: owner, WorkID: work.ID, Question: "may I proceed?", Now: now.Add(5 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if approval.Status != "pending" {
		t.Fatalf("approval = %+v", approval)
	}
	var approvalReceiptsBefore, approvalReceiptsAfter int
	if err := database.db.QueryRow(`SELECT count(*) FROM work_requests`).Scan(&approvalReceiptsBefore); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ResolveWorkApproval(context.Background(), ResolveWorkApprovalParams{RequestID: uuid.NewString(), Actor: Principal{Kind: "agent", ID: agent.ID, OrganizationID: bootstrap.Organization.ID}, ApprovalID: approval.ID, Decision: "approved", Now: now.Add(6 * time.Second)}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("agent approval decision = %v", err)
	}
	if err := database.db.QueryRow(`SELECT count(*) FROM work_requests`).Scan(&approvalReceiptsAfter); err != nil {
		t.Fatal(err)
	}
	if approvalReceiptsAfter != approvalReceiptsBefore {
		t.Fatalf("denied approval created receipt: %d/%d", approvalReceiptsBefore, approvalReceiptsAfter)
	}
	auditEvents, err := database.ListAuditEvents(context.Background(), ListAuditEventsParams{OrganizationID: bootstrap.Organization.ID, Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	foundDeniedApproval := false
	for _, event := range auditEvents {
		if event.Action == AuditWorkApprovalResolve && event.Outcome == "denied" && event.ReasonCode == "human_required" {
			foundDeniedApproval = true
		}
	}
	if !foundDeniedApproval {
		t.Fatal("missing human-only approval denial audit")
	}
	resolved, err := database.ResolveWorkApproval(context.Background(), ResolveWorkApprovalParams{RequestID: uuid.NewString(), Actor: owner, ApprovalID: approval.ID, Decision: "approved", Now: now.Add(7 * time.Second)})
	if err != nil || resolved.Status != "approved" {
		t.Fatalf("resolved approval = %+v, %v", resolved, err)
	}
	criterion := work.AcceptanceCriteria[0]
	completed, err := database.TransitionWork(context.Background(), TransitionWorkParams{RequestID: uuid.NewString(), Actor: owner, WorkID: work.ID, ToState: WorkStateCompleted, Result: "done", CriterionResults: []WorkCriterionResultInput{{CriterionID: criterion.ID, Verdict: "passed", Evidence: "result"}}, Now: now.Add(8 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if completed.State != WorkStateCompleted || completed.CompletedAt == nil {
		t.Fatalf("completed work = %+v", completed)
	}
	var endedAt int64
	if err := database.db.QueryRow(`SELECT ended_at FROM work_assignments WHERE id = ?`, assignment.ID).Scan(&endedAt); err != nil {
		t.Fatal(err)
	}
	if endedAt == 0 {
		t.Fatal("terminal work did not end assignment")
	}
	var endedEvents int
	if err := database.db.QueryRow(`SELECT count(*) FROM work_events WHERE work_id = ? AND event_kind = 'assignment.ended' AND reference_id = ?`, work.ID, assignment.ID).Scan(&endedEvents); err != nil {
		t.Fatal(err)
	}
	if endedEvents != 1 {
		t.Fatalf("assignment ended events = %d", endedEvents)
	}
	if _, err := database.TransitionWork(context.Background(), TransitionWorkParams{RequestID: uuid.NewString(), Actor: owner, WorkID: work.ID, ToState: WorkStateFailed, Result: "late", Now: now.Add(9 * time.Second)}); !errors.Is(err, ErrWorkTransitionInvalid) {
		t.Fatalf("terminal transition = %v", err)
	}
	var eventCount, receiptCount int
	if err := database.db.QueryRow(`SELECT count(*) FROM work_events WHERE work_id = ?`, work.ID).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRow(`SELECT count(*) FROM work_requests`).Scan(&receiptCount); err != nil {
		t.Fatal(err)
	}
	if eventCount < 9 || receiptCount != 6 {
		t.Fatalf("work facts counts = events %d receipts %d", eventCount, receiptCount)
	}
	rows, err := database.db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("work facts left a foreign-key violation")
	}
}

func TestWorkTransitionKnowledgeDirtyFailurePreservesWorkFacts(t *testing.T) {
	database, owner, _, _, now := openCollaborationFixture(t, filepath.Join(t.TempDir(), "server.db"))
	defer database.Close()
	space, err := database.CreateGroup(context.Background(), CreateGroupParams{RequestID: uuid.NewString(), Actor: owner, Name: "work dirty", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	message, err := database.SendMessage(context.Background(), SendMessageParams{RequestID: uuid.NewString(), Actor: owner, Target: MessageTarget{Kind: MessageTargetSpace, ID: space.ID}, Body: "delegate", Now: now.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	create := WorkCreateParams{
		RequestID: uuid.NewString(), Actor: owner, SourceMessageID: message.ID, SourceSpaceID: space.ID,
		SourceTarget: message.Target, SourceTargetSequence: message.TargetSequence,
		Goal: "work exact", AcceptanceCriteria: []string{"done"}, Now: now.Add(2 * time.Second),
	}
	work, err := database.CreateWork(context.Background(), create)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateWork(context.Background(), create); err != nil {
		t.Fatal(err)
	}
	assertKnowledgeDirty(t, database, KnowledgeSource{Kind: KnowledgeSourceWork, ID: work.ID}, knowledgeWorkRevision(work), 1)
	transition := TransitionWorkParams{RequestID: uuid.NewString(), Actor: owner, WorkID: work.ID, ToState: WorkStateBlocked, Reason: "blocked", Now: now.Add(3 * time.Second)}
	updated, err := database.TransitionWork(context.Background(), transition)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.TransitionWork(context.Background(), transition); err != nil {
		t.Fatal(err)
	}
	assertKnowledgeDirty(t, database, KnowledgeSource{Kind: KnowledgeSourceWork, ID: work.ID}, knowledgeWorkRevision(updated), 2)
	beforeRevision := knowledgeWorkRevision(updated)
	beforeDirty := knowledgeDirtyCount(t, database, work.ID)
	if _, err := database.db.Exec(`CREATE TRIGGER fail_work_dirty BEFORE INSERT ON knowledge_dirty_sources BEGIN SELECT RAISE(ABORT, 'work dirty failed'); END`); err != nil {
		t.Fatal(err)
	}
	failed := TransitionWorkParams{RequestID: uuid.NewString(), Actor: owner, WorkID: work.ID, ToState: WorkStateOpen, Now: now.Add(4 * time.Second)}
	if _, err := database.TransitionWork(context.Background(), failed); err == nil || !strings.Contains(err.Error(), "work dirty failed") {
		t.Fatalf("work dirty rollback = %v", err)
	}
	if _, err := database.db.Exec(`DROP TRIGGER fail_work_dirty`); err != nil {
		t.Fatal(err)
	}
	detail, err := database.GetWorkDetail(context.Background(), WorkReadParams{Actor: owner, WorkID: work.ID, Now: now.Add(5 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	current := detail.Work
	if current.State != updated.State || knowledgeWorkRevision(current) != beforeRevision || knowledgeDirtyCount(t, database, work.ID) != beforeDirty {
		t.Fatalf("failed transition changed work facts: current=%+v before state=%s revision=%x dirty=%d", current, updated.State, beforeRevision, beforeDirty)
	}
}

func TestEndWorkAssignmentsIterationErrorRollsBackAssignmentAndEvents(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 7, 22, 13, 0, 0, 0, time.UTC)
	bootstrap, err := database.EnsureAuthority(context.Background(), "work-iteration-bootstrap-credential-abcdefghijklmnopqrstuvwxyz", now)
	if err != nil {
		t.Fatal(err)
	}
	owner := Principal{Kind: "human", ID: bootstrap.Human.ID, OrganizationID: bootstrap.Organization.ID}
	space, err := database.CreateGroup(context.Background(), CreateGroupParams{RequestID: uuid.NewString(), Actor: owner, Name: "assignment iteration", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	source, err := database.SendMessage(context.Background(), SendMessageParams{RequestID: uuid.NewString(), Actor: owner, Target: MessageTarget{Kind: MessageTargetSpace, ID: space.ID}, Body: "delegate", Now: now.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := database.CreateAgent(context.Background(), CreateAgentParams{RequestID: uuid.NewString(), Actor: owner, Name: "iteration worker", Description: "worker", Driver: "native", Now: now.Add(2 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	computerID := uuid.NewString()
	if _, err := database.db.Exec(`INSERT INTO computers(id, registration_key_hash, name, os, arch, created_at, last_seen_at) VALUES(?, zeroblob(32), 'computer', 'linux', 'amd64', ?, ?)`, computerID, unixNano(now), unixNano(now)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.Exec(`INSERT INTO agent_placements(agent_id, computer_id, generation, state, error_code, created_at, updated_at) VALUES(?, ?, 1, 'active', '', ?, ?)`, agent.ID, computerID, unixNano(now), unixNano(now)); err != nil {
		t.Fatal(err)
	}
	work, err := database.CreateWork(context.Background(), WorkCreateParams{RequestID: uuid.NewString(), Actor: owner, SourceMessageID: source.ID, SourceSpaceID: space.ID, SourceTarget: source.Target, SourceTargetSequence: source.TargetSequence, Goal: "iteration safety", AcceptanceCriteria: []string{"done"}, Now: now.Add(3 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	assignment, err := database.AssignWork(context.Background(), AssignWorkParams{RequestID: uuid.NewString(), Actor: owner, WorkID: work.ID, Role: WorkAssignmentCoordinator, AgentID: agent.ID, Now: now.Add(4 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	contextWithIterationError := context.WithValue(context.Background(), workAssignmentRowsErrorContextKey{}, workAssignmentRowsErrorFunc(func(rows *sql.Rows) error {
		return errors.New("forced assignment iteration error")
	}))
	tx, err := database.db.BeginTx(contextWithIterationError, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := endWorkAssignments(contextWithIterationError, tx, work.ID, "", "", owner, "completed", now.Add(5*time.Second)); err == nil || !strings.Contains(err.Error(), "forced assignment iteration error") {
		tx.Rollback()
		t.Fatalf("end assignments with iteration error = %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var endedAt int64
	if err := database.db.QueryRow(`SELECT COALESCE(ended_at, 0) FROM work_assignments WHERE id = ?`, assignment.ID).Scan(&endedAt); err != nil {
		t.Fatal(err)
	}
	var endedEvents int
	if err := database.db.QueryRow(`SELECT count(*) FROM work_events WHERE work_id = ? AND event_kind = 'assignment.ended' AND reference_id = ?`, work.ID, assignment.ID).Scan(&endedEvents); err != nil {
		t.Fatal(err)
	}
	if endedAt != 0 || endedEvents != 0 {
		t.Fatalf("iteration failure committed assignment/event facts: ended_at=%d events=%d", endedAt, endedEvents)
	}
}

func TestWorkPartsIterationErrorsRejectIncompleteWorkAndRollbackCreate(t *testing.T) {
	for _, source := range []string{workRowsConstraints, workRowsCriteria} {
		t.Run(source, func(t *testing.T) {
			database, owner, _, _, now := openCollaborationFixture(t, filepath.Join(t.TempDir(), "server.db"))
			defer database.Close()
			space, err := database.CreateGroup(context.Background(), CreateGroupParams{RequestID: uuid.NewString(), Actor: owner, Name: "work parts iteration", Now: now})
			if err != nil {
				t.Fatal(err)
			}
			message, err := database.SendMessage(context.Background(), SendMessageParams{RequestID: uuid.NewString(), Actor: owner, Target: MessageTarget{Kind: MessageTargetSpace, ID: space.ID}, Body: "delegate", Now: now.Add(time.Second)})
			if err != nil {
				t.Fatal(err)
			}
			create := WorkCreateParams{RequestID: uuid.NewString(), Actor: owner, SourceMessageID: message.ID, SourceSpaceID: space.ID, SourceTarget: message.Target, SourceTargetSequence: message.TargetSequence, Goal: "work parts", Constraints: []string{"constraint"}, AcceptanceCriteria: []string{"criterion"}, Now: now.Add(2 * time.Second)}
			work, err := database.CreateWork(context.Background(), create)
			if err != nil {
				t.Fatal(err)
			}
			contextWithIterationError := context.WithValue(context.Background(), workRowsErrorContextKey{}, workRowsErrorFunc(func(actual string, rows *sql.Rows) error {
				if actual == source {
					return errors.New("forced " + source + " iteration error")
				}
				return rows.Err()
			}))
			if _, err := database.GetWorkDetail(contextWithIterationError, WorkReadParams{Actor: owner, WorkID: work.ID, Now: now.Add(3 * time.Second)}); err == nil || !strings.Contains(err.Error(), "forced "+source+" iteration error") {
				t.Fatalf("get work with %s iteration error = %v", source, err)
			}
			before := readWorkFactCounts(t, database)
			create.RequestID = uuid.NewString()
			create.Goal = "work parts rollback"
			create.Now = now.Add(4 * time.Second)
			if _, err := database.CreateWork(contextWithIterationError, create); err == nil || !strings.Contains(err.Error(), "forced "+source+" iteration error") {
				t.Fatalf("create work with %s iteration error = %v", source, err)
			}
			after := readWorkFactCounts(t, database)
			if after != before {
				t.Fatalf("%s iteration error committed work facts: after=%+v before=%+v", source, after, before)
			}
		})
	}
}

func TestWorkCompletionCriteriaIterationErrorRollsBackTransition(t *testing.T) {
	database, owner, _, _, now := openCollaborationFixture(t, filepath.Join(t.TempDir(), "server.db"))
	defer database.Close()
	space, err := database.CreateGroup(context.Background(), CreateGroupParams{RequestID: uuid.NewString(), Actor: owner, Name: "completion iteration", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	message, err := database.SendMessage(context.Background(), SendMessageParams{RequestID: uuid.NewString(), Actor: owner, Target: MessageTarget{Kind: MessageTargetSpace, ID: space.ID}, Body: "delegate", Now: now.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	work, err := database.CreateWork(context.Background(), WorkCreateParams{RequestID: uuid.NewString(), Actor: owner, SourceMessageID: message.ID, SourceSpaceID: space.ID, SourceTarget: message.Target, SourceTargetSequence: message.TargetSequence, Goal: "completion iteration", AcceptanceCriteria: []string{"first", "second"}, Now: now.Add(2 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	var receiptsBefore, eventsBefore int
	if err := database.db.QueryRow(`SELECT count(*) FROM work_requests`).Scan(&receiptsBefore); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRow(`SELECT count(*) FROM work_events WHERE work_id = ?`, work.ID).Scan(&eventsBefore); err != nil {
		t.Fatal(err)
	}
	contextWithIterationError := context.WithValue(context.Background(), workRowsErrorContextKey{}, workRowsErrorFunc(func(source string, rows *sql.Rows) error {
		if source == workRowsCompletion {
			return errors.New("forced completion criteria iteration error")
		}
		return rows.Err()
	}))
	results := []WorkCriterionResultInput{
		{CriterionID: work.AcceptanceCriteria[0].ID, Verdict: "passed", Evidence: "first evidence"},
		{CriterionID: work.AcceptanceCriteria[1].ID, Verdict: "passed", Evidence: "second evidence"},
	}
	if _, err := database.TransitionWork(contextWithIterationError, TransitionWorkParams{RequestID: uuid.NewString(), Actor: owner, WorkID: work.ID, ToState: WorkStateCompleted, Result: "done", CriterionResults: results, Now: now.Add(3 * time.Second)}); err == nil || !strings.Contains(err.Error(), "forced completion criteria iteration error") {
		t.Fatalf("complete work with criteria iteration error = %v", err)
	}
	detail, err := database.GetWorkDetail(context.Background(), WorkReadParams{Actor: owner, WorkID: work.ID, Now: now.Add(4 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	current := detail.Work
	if current.State != WorkStateOpen || current.CompletedAt != nil {
		t.Fatalf("criteria iteration error completed work: %+v", current)
	}
	var receiptsAfter, eventsAfter, criterionResults int
	if err := database.db.QueryRow(`SELECT count(*) FROM work_requests`).Scan(&receiptsAfter); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRow(`SELECT count(*) FROM work_events WHERE work_id = ?`, work.ID).Scan(&eventsAfter); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRow(`SELECT count(*) FROM work_acceptance_results WHERE work_id = ?`, work.ID).Scan(&criterionResults); err != nil {
		t.Fatal(err)
	}
	if receiptsAfter != receiptsBefore || eventsAfter != eventsBefore || criterionResults != 0 {
		t.Fatalf("criteria iteration error committed transition facts: receipts %d/%d events %d/%d results %d", receiptsAfter, receiptsBefore, eventsAfter, eventsBefore, criterionResults)
	}
}

func TestWorkMutationReplayIgnoresServerNowAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.db")
	database, owner, _, _, now := openCollaborationFixture(t, path)
	space, err := database.CreateGroup(context.Background(), CreateGroupParams{RequestID: uuid.NewString(), Actor: owner, Name: "replay now", Now: now})
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	source, err := database.SendMessage(context.Background(), SendMessageParams{RequestID: uuid.NewString(), Actor: owner, Target: MessageTarget{Kind: MessageTargetSpace, ID: space.ID}, Body: "replay source", Now: now.Add(time.Second)})
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	agent, err := database.CreateAgent(context.Background(), CreateAgentParams{RequestID: uuid.NewString(), Actor: owner, Name: "replay agent", Description: "agent", Driver: "native", Now: now.Add(2 * time.Second)})
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	computerID := uuid.NewString()
	if _, err := database.db.Exec(`INSERT INTO computers(id, registration_key_hash, name, os, arch, created_at, last_seen_at) VALUES(?, zeroblob(32), 'computer', 'linux', 'amd64', ?, ?)`, computerID, unixNano(now), unixNano(now)); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if _, err := database.db.Exec(`INSERT INTO agent_placements(agent_id, computer_id, generation, state, error_code, created_at, updated_at) VALUES(?, ?, 1, 'active', '', ?, ?)`, agent.ID, computerID, unixNano(now), unixNano(now)); err != nil {
		database.Close()
		t.Fatal(err)
	}

	create := WorkCreateParams{RequestID: uuid.NewString(), Actor: owner, SourceMessageID: source.ID, SourceSpaceID: source.SpaceID, SourceTarget: source.Target, SourceTargetSequence: source.TargetSequence, Goal: "replay durable work", Constraints: []string{"constraint"}, AcceptanceCriteria: []string{"criterion"}, Now: now.Add(3 * time.Second)}
	created, err := database.CreateWork(context.Background(), create)
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	createCounts := readWorkReplayFactCounts(t, database, created.ID)
	create.Now = now.Add(30 * time.Second)
	if replay, err := database.CreateWork(context.Background(), create); err != nil || replay.ID != created.ID {
		database.Close()
		t.Fatalf("create same-process replay = %+v, %v", replay, err)
	}
	assertWorkReplayFactCountsUnchanged(t, database, created.ID, "create same-process replay", createCounts)
	changedCreate := create
	changedCreate.Goal = "changed goal"
	if _, err := database.CreateWork(context.Background(), changedCreate); !errors.Is(err, ErrWorkRequestConflict) {
		database.Close()
		t.Fatalf("create changed payload = %v", err)
	}
	assertWorkReplayFactCountsUnchanged(t, database, created.ID, "create changed payload", createCounts)

	assign := AssignWorkParams{RequestID: uuid.NewString(), Actor: owner, WorkID: created.ID, Role: WorkAssignmentCoordinator, AgentID: agent.ID, Now: now.Add(4 * time.Second)}
	assignment, err := database.AssignWork(context.Background(), assign)
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	assignCounts := readWorkReplayFactCounts(t, database, created.ID)
	assign.Now = now.Add(40 * time.Second)
	if replay, err := database.AssignWork(context.Background(), assign); err != nil || replay.ID != assignment.ID {
		database.Close()
		t.Fatalf("assign same-process replay = %+v, %v", replay, err)
	}
	assertWorkReplayFactCountsUnchanged(t, database, created.ID, "assign same-process replay", assignCounts)
	changedAssign := assign
	changedAssign.Role = WorkAssignmentContributor
	if _, err := database.AssignWork(context.Background(), changedAssign); !errors.Is(err, ErrWorkRequestConflict) {
		database.Close()
		t.Fatalf("assign changed payload = %v", err)
	}
	assertWorkReplayFactCountsUnchanged(t, database, created.ID, "assign changed payload", assignCounts)

	transition := TransitionWorkParams{RequestID: uuid.NewString(), Actor: owner, WorkID: created.ID, ToState: WorkStateBlocked, Reason: "needs review", CriterionResults: []WorkCriterionResultInput{{CriterionID: created.AcceptanceCriteria[0].ID, Verdict: "passed", Evidence: "evidence"}}, Now: now.Add(5 * time.Second)}
	if _, err := database.TransitionWork(context.Background(), transition); err != nil {
		database.Close()
		t.Fatal(err)
	}
	transitionCounts := readWorkReplayFactCounts(t, database, created.ID)
	transition.Now = now.Add(50 * time.Second)
	if _, err := database.TransitionWork(context.Background(), transition); err != nil {
		database.Close()
		t.Fatalf("transition same-process replay = %v", err)
	}
	assertWorkReplayFactCountsUnchanged(t, database, created.ID, "transition same-process replay", transitionCounts)
	changedTransition := transition
	changedTransition.Reason = "changed reason"
	if _, err := database.TransitionWork(context.Background(), changedTransition); !errors.Is(err, ErrWorkRequestConflict) {
		database.Close()
		t.Fatalf("transition changed payload = %v", err)
	}
	assertWorkReplayFactCountsUnchanged(t, database, created.ID, "transition changed payload", transitionCounts)

	request := RequestWorkApprovalParams{RequestID: uuid.NewString(), Actor: owner, WorkID: created.ID, Question: "approve?", Now: now.Add(6 * time.Second)}
	pending, err := database.RequestWorkApproval(context.Background(), request)
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	requestCounts := readWorkReplayFactCounts(t, database, created.ID)
	request.Now = now.Add(60 * time.Second)
	if replay, err := database.RequestWorkApproval(context.Background(), request); err != nil || replay.ID != pending.ID || replay.Status != "pending" {
		database.Close()
		t.Fatalf("request approval same-process replay = %+v, %v", replay, err)
	}
	assertWorkReplayFactCountsUnchanged(t, database, created.ID, "request approval same-process replay", requestCounts)
	changedRequest := request
	changedRequest.Question = "changed question"
	if _, err := database.RequestWorkApproval(context.Background(), changedRequest); !errors.Is(err, ErrWorkRequestConflict) {
		database.Close()
		t.Fatalf("request approval changed payload = %v", err)
	}
	assertWorkReplayFactCountsUnchanged(t, database, created.ID, "request approval changed payload", requestCounts)

	resolve := ResolveWorkApprovalParams{RequestID: uuid.NewString(), Actor: owner, ApprovalID: pending.ID, Decision: "rejected", Note: "needs changes", Now: now.Add(7 * time.Second)}
	resolved, err := database.ResolveWorkApproval(context.Background(), resolve)
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	resolveCounts := readWorkReplayFactCounts(t, database, created.ID)
	resolve.Now = now.Add(70 * time.Second)
	if replay, err := database.ResolveWorkApproval(context.Background(), resolve); err != nil || replay.ID != resolved.ID || replay.Status != "rejected" {
		database.Close()
		t.Fatalf("resolve approval same-process replay = %+v, %v", replay, err)
	}
	assertWorkReplayFactCountsUnchanged(t, database, created.ID, "resolve approval same-process replay", resolveCounts)
	changedResolve := resolve
	changedResolve.Note = "changed note"
	if _, err := database.ResolveWorkApproval(context.Background(), changedResolve); !errors.Is(err, ErrWorkRequestConflict) {
		database.Close()
		t.Fatalf("resolve approval changed payload = %v", err)
	}
	assertWorkReplayFactCountsUnchanged(t, database, created.ID, "resolve approval changed payload", resolveCounts)

	before := readWorkReplayFactCounts(t, database, created.ID)
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	create.Now = now.Add(80 * time.Second)
	assign.Now = now.Add(81 * time.Second)
	transition.Now = now.Add(82 * time.Second)
	request.Now = now.Add(83 * time.Second)
	resolve.Now = now.Add(84 * time.Second)
	createReopenCounts := readWorkReplayFactCounts(t, database, created.ID)
	if replay, err := database.CreateWork(context.Background(), create); err != nil || replay.ID != created.ID {
		t.Fatalf("create reopen replay = %+v, %v", replay, err)
	}
	assertWorkReplayFactCountsUnchanged(t, database, created.ID, "create reopen replay", createReopenCounts)
	assignReopenCounts := readWorkReplayFactCounts(t, database, created.ID)
	if replay, err := database.AssignWork(context.Background(), assign); err != nil || replay.ID != assignment.ID {
		t.Fatalf("assign reopen replay = %+v, %v", replay, err)
	}
	assertWorkReplayFactCountsUnchanged(t, database, created.ID, "assign reopen replay", assignReopenCounts)
	transitionReopenCounts := readWorkReplayFactCounts(t, database, created.ID)
	if _, err := database.TransitionWork(context.Background(), transition); err != nil {
		t.Fatalf("transition reopen replay = %v", err)
	}
	assertWorkReplayFactCountsUnchanged(t, database, created.ID, "transition reopen replay", transitionReopenCounts)
	requestReopenCounts := readWorkReplayFactCounts(t, database, created.ID)
	if replay, err := database.RequestWorkApproval(context.Background(), request); err != nil || replay.ID != pending.ID {
		t.Fatalf("request approval reopen replay = %+v, %v", replay, err)
	}
	assertWorkReplayFactCountsUnchanged(t, database, created.ID, "request approval reopen replay", requestReopenCounts)
	resolveReopenCounts := readWorkReplayFactCounts(t, database, created.ID)
	if replay, err := database.ResolveWorkApproval(context.Background(), resolve); err != nil || replay.ID != resolved.ID {
		t.Fatalf("resolve approval reopen replay = %+v, %v", replay, err)
	}
	assertWorkReplayFactCountsUnchanged(t, database, created.ID, "resolve approval reopen replay", resolveReopenCounts)
	if after := readWorkReplayFactCounts(t, database, created.ID); after != before {
		t.Fatalf("replay duplicated durable facts: before=%+v after=%+v", before, after)
	}
}

type workReplayFactCounts struct {
	works, assignments, approvals, results, events, receipts, dirty int
}

func readWorkReplayFactCounts(t *testing.T, database *Store, workID string) workReplayFactCounts {
	t.Helper()
	var counts workReplayFactCounts
	for _, item := range []struct {
		query string
		into  *int
	}{
		{`SELECT count(*) FROM works`, &counts.works},
		{`SELECT count(*) FROM work_assignments WHERE work_id = ?`, &counts.assignments},
		{`SELECT count(*) FROM work_approvals WHERE work_id = ?`, &counts.approvals},
		{`SELECT count(*) FROM work_acceptance_results WHERE work_id = ?`, &counts.results},
		{`SELECT count(*) FROM work_events WHERE work_id = ?`, &counts.events},
		{`SELECT count(*) FROM work_requests`, &counts.receipts},
		{`SELECT count(*) FROM knowledge_dirty_sources WHERE source_kind = 'work' AND source_id = ?`, &counts.dirty},
	} {
		args := []any{}
		if strings.Contains(item.query, "?") {
			args = append(args, workID)
		}
		if err := database.db.QueryRow(item.query, args...).Scan(item.into); err != nil {
			t.Fatal(err)
		}
	}
	return counts
}

func assertWorkReplayFactCountsUnchanged(t *testing.T, database *Store, workID, stage string, before workReplayFactCounts) {
	t.Helper()
	if after := readWorkReplayFactCounts(t, database, workID); after != before {
		t.Fatalf("%s changed durable facts: before=%+v after=%+v", stage, before, after)
	}
}

type workFactCounts struct {
	works, spaces, events, receipts, dirty int
}

func readWorkFactCounts(t *testing.T, database *Store) workFactCounts {
	t.Helper()
	var counts workFactCounts
	for _, count := range []struct {
		query string
		into  *int
	}{
		{query: `SELECT count(*) FROM works`, into: &counts.works},
		{query: `SELECT count(*) FROM spaces`, into: &counts.spaces},
		{query: `SELECT count(*) FROM work_events`, into: &counts.events},
		{query: `SELECT count(*) FROM work_requests`, into: &counts.receipts},
		{query: `SELECT count(*) FROM knowledge_dirty_sources`, into: &counts.dirty},
	} {
		if err := database.db.QueryRow(count.query).Scan(count.into); err != nil {
			t.Fatal(err)
		}
	}
	return counts
}
