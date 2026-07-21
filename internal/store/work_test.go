package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
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
	allowed, err := database.CheckPermission(context.Background(), Principal{Kind: "agent", ID: agent.ID, OrganizationID: bootstrap.Organization.ID}, CapabilityWorkManage, Scope{Kind: "work", ID: work.ID}, now.Add(3*time.Second))
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
	auditEvents, err := database.ListAuditEvents(context.Background(), bootstrap.Organization.ID, 0, 1000)
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

func TestWorkMigrationDownDropsWorkFactsAndRestoresGrantSchema(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var version int
	if err := database.db.QueryRow(`SELECT version_id FROM goose_db_version WHERE is_applied = 1 ORDER BY version_id DESC LIMIT 1`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 15 {
		t.Fatalf("migration version = %d", version)
	}
	if _, err := database.db.Exec(`UPDATE work_constraints SET body = 'changed' WHERE 1 = 0`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.Exec(`SELECT 1 FROM works LIMIT 1`); err != nil {
		t.Fatal(err)
	}
	goose.SetBaseFS(migrations)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	if err := goose.Down(database.db, "migrations"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.Exec(`SELECT 1 FROM works LIMIT 1`); err == nil {
		t.Fatal("works table survived down migration")
	}
	if _, err := database.db.Exec(`INSERT INTO grants(id, organization_id, subject_kind, subject_id, issuer_kind, issuer_id, capability, scope_kind, scope_id, parent_grant_id, created_at, updated_at) VALUES(?, ?, 'human', ?, 'system', '', 'organization.admin', 'work', ?, '', ?, ?)`, uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), 1, 1); err == nil {
		t.Fatal("down migration still accepts work scope")
	}
}
