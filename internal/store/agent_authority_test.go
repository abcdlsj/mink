package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
)

func TestAgentAndPlacementMutationsRequireCurrentGrantAndPreserveReceipts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.db")
	database, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 20, 15, 0, 0, 123456789, time.UTC)
	bootstrap, err := database.EnsureAuthority(context.Background(), "agent-authority-owner-credential-abcdefghijklmnopqrstuvwxyz", now)
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	owner := Principal{Kind: "human", ID: bootstrap.Human.ID, OrganizationID: bootstrap.Organization.ID}
	peerHuman, err := database.CreateHuman(context.Background(), CreateHumanParams{
		RequestID: uuid.NewString(), Actor: owner, Name: "Agent Operator", Role: "member",
		Credential: "agent-authority-peer-credential-abcdefghijklmnopqrstuvwxyz", Now: now.Add(time.Second),
	})
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	peer := Principal{Kind: "human", ID: peerHuman.ID, OrganizationID: owner.OrganizationID}

	deniedCreate := CreateAgentParams{RequestID: uuid.NewString(), Actor: peer, Name: "denied-agent", Driver: "native", Now: now.Add(2 * time.Second)}
	if _, err := database.CreateAgent(context.Background(), deniedCreate); !errors.Is(err, ErrPermissionDenied) {
		database.Close()
		t.Fatalf("create without grant error = %v", err)
	}
	if got := tableCount(t, database, "agents"); got != 0 {
		database.Close()
		t.Fatalf("denied create persisted %d agents", got)
	}
	if got := tableCount(t, database, "agent_create_requests"); got != 0 {
		database.Close()
		t.Fatalf("denied create persisted %d receipts", got)
	}
	assertAuthorityAudit(t, database, owner.OrganizationID, AuditAgentCreate, "agent", "", "", "", "denied", "permission_missing")

	createGrant := issueAgentAuthorityGrant(t, database, owner, peer, CapabilityAgentCreate, Scope{Kind: "organization", ID: owner.OrganizationID}, bootstrap.RootGrant.ID, now.Add(3*time.Second))
	createParams := CreateAgentParams{RequestID: uuid.NewString(), Actor: peer, Name: "delegated-agent", Description: "delegated", Driver: "codex", Now: now.Add(4 * time.Second)}
	agent, err := database.CreateAgent(context.Background(), createParams)
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	assertAuthorityAudit(t, database, owner.OrganizationID, AuditAgentCreate, "agent", agent.ID, "", "", "committed", "")
	if _, err := database.CreateAgent(context.Background(), CreateAgentParams{
		RequestID: createParams.RequestID, Actor: owner, Name: createParams.Name,
		Description: createParams.Description, Driver: createParams.Driver, Now: now.Add(5 * time.Second),
	}); !errors.Is(err, ErrAgentRequestConflict) {
		database.Close()
		t.Fatalf("cross-actor create replay error = %v", err)
	}
	if _, err := database.RevokeGrant(context.Background(), RevokeGrantParams{
		RequestID: uuid.NewString(), Actor: owner, GrantID: createGrant.ID, Now: now.Add(6 * time.Second),
	}); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if _, err := database.CreateAgent(context.Background(), createParams); !errors.Is(err, ErrPermissionDenied) {
		database.Close()
		t.Fatalf("revoked create replay error = %v", err)
	}

	computerKey := "agent-placement-computer-key-abcdefghijklmnopqrstuvwxyz"
	computer, err := database.RegisterComputer(context.Background(), RegisterComputerParams{
		RegistrationKey: computerKey, Name: "placement-host",
		OS: "linux", Arch: "amd64", Now: now.Add(7 * time.Second),
	})
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	placeParams := SetAgentPlacementParams{
		RequestID: uuid.NewString(), Actor: owner, AgentID: agent.ID, ComputerID: computer.ID, Now: now.Add(8 * time.Second),
	}
	placed, err := database.SetAgentPlacement(context.Background(), placeParams)
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	assertAuthorityAudit(t, database, owner.OrganizationID, AuditAgentPlace, "agent", agent.ID, "computer", computer.ID, "committed", "")
	active, err := database.AcknowledgeAgentPlacement(context.Background(), AcknowledgePlacementParams{
		ComputerID: computer.ID, RegistrationKey: computerKey, AgentID: agent.ID,
		Generation: placed.Generation, State: "active", Now: now.Add(9 * time.Second),
	})
	if err != nil || active.State != "active" {
		database.Close()
		t.Fatalf("placement acknowledgement = %+v, %v", active, err)
	}
	replayed, err := database.SetAgentPlacement(context.Background(), placeParams)
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	if replayed != placed || replayed.State != "pending" {
		database.Close()
		t.Fatalf("placement replay = %+v, want %+v", replayed, placed)
	}
	if got := countAuditAction(t, database, owner.OrganizationID, AuditAgentPlace, "committed"); got != 1 {
		database.Close()
		t.Fatalf("placement replay audits = %d", got)
	}

	secondAgent, err := database.CreateAgent(context.Background(), CreateAgentParams{
		RequestID: uuid.NewString(), Actor: owner, Name: "placement-target", Driver: "claude", Now: now.Add(10 * time.Second),
	})
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	deniedPlace := SetAgentPlacementParams{
		RequestID: uuid.NewString(), Actor: peer, AgentID: secondAgent.ID, ComputerID: computer.ID, Now: now.Add(11 * time.Second),
	}
	if _, err := database.SetAgentPlacement(context.Background(), deniedPlace); !errors.Is(err, ErrPermissionDenied) {
		database.Close()
		t.Fatalf("placement without grant error = %v", err)
	}
	if _, err := database.GetAgentPlacement(context.Background(), secondAgent.ID); !errors.Is(err, ErrPlacementNotFound) {
		database.Close()
		t.Fatalf("denied placement fact error = %v", err)
	}
	assertAuthorityAudit(t, database, owner.OrganizationID, AuditAgentPlace, "agent", secondAgent.ID, "computer", computer.ID, "denied", "permission_missing")
	placeGrant := issueAgentAuthorityGrant(t, database, owner, peer, CapabilityAgentPlace, Scope{Kind: "agent", ID: secondAgent.ID}, bootstrap.RootGrant.ID, now.Add(12*time.Second))
	allowedPlace, err := database.SetAgentPlacement(context.Background(), SetAgentPlacementParams{
		RequestID: deniedPlace.RequestID, Actor: peer, AgentID: secondAgent.ID, ComputerID: computer.ID, Now: now.Add(13 * time.Second),
	})
	if err != nil || allowedPlace.AgentID != secondAgent.ID {
		database.Close()
		t.Fatalf("delegated placement = %+v, %v", allowedPlace, err)
	}
	if _, err := database.RevokeGrant(context.Background(), RevokeGrantParams{
		RequestID: uuid.NewString(), Actor: owner, GrantID: placeGrant.ID, Now: now.Add(14 * time.Second),
	}); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if _, err := database.SetAgentPlacement(context.Background(), SetAgentPlacementParams{
		RequestID: deniedPlace.RequestID, Actor: peer, AgentID: secondAgent.ID, ComputerID: computer.ID, Now: now.Add(15 * time.Second),
	}); !errors.Is(err, ErrPermissionDenied) {
		database.Close()
		t.Fatalf("revoked placement replay error = %v", err)
	}
	if _, err := database.SetAgentPlacement(context.Background(), SetAgentPlacementParams{
		RequestID: deniedPlace.RequestID, Actor: owner, AgentID: secondAgent.ID, ComputerID: computer.ID, Now: now.Add(16 * time.Second),
	}); !errors.Is(err, ErrPlacementRequestConflict) {
		database.Close()
		t.Fatalf("cross-actor placement replay error = %v", err)
	}
	if _, err := database.SetHumanStatus(context.Background(), SetHumanStatusParams{
		RequestID: uuid.NewString(), Actor: owner, HumanID: peer.ID, Status: "disabled", Now: now.Add(17 * time.Second),
	}); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if _, err := database.CreateAgent(context.Background(), CreateAgentParams{
		RequestID: uuid.NewString(), Actor: peer, Name: "disabled-create", Driver: "native", Now: now.Add(18 * time.Second),
	}); !errors.Is(err, ErrPermissionDenied) {
		database.Close()
		t.Fatalf("disabled actor create error = %v", err)
	}
	assertAuthorityAudit(t, database, owner.OrganizationID, AuditAgentCreate, "agent", "", "", "", "denied", "principal_inactive")

	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	restarted, err := database.SetAgentPlacement(context.Background(), placeParams)
	if err != nil || restarted != placed {
		t.Fatalf("placement restart replay = %+v, %v", restarted, err)
	}
}

func TestOpenUpgradesVersionSevenAuthorityReceiptsAndAuditSequence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.db")
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if err := configure(database); err != nil {
		database.Close()
		t.Fatal(err)
	}
	goose.SetBaseFS(migrations)
	if err := goose.SetDialect("sqlite3"); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := goose.UpTo(database, "migrations", 7); err != nil {
		database.Close()
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 20, 16, 0, 0, 123456789, time.UTC)
	stamp := unixNano(now)
	organizationID, humanID, grantID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	agentID, requestID, auditID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	credentialHash := sha256.Sum256([]byte("legacy-owner-credential"))
	legacyFingerprint := sha256.Sum256([]byte("legacy-anonymous-agent-receipt"))
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO organizations(singleton, id, name, bootstrap_human_id, created_at) VALUES(1, ?, 'Sumi', ?, ?)`, []any{organizationID, humanID, stamp}},
		{`INSERT INTO humans(id, organization_id, name, role, status, credential_hash, created_at, updated_at) VALUES(?, ?, 'Owner', 'owner', 'active', ?, ?, ?)`, []any{humanID, organizationID, credentialHash[:], stamp, stamp}},
		{`INSERT INTO grants(id, organization_id, subject_kind, subject_id, issuer_kind, issuer_id, capability, scope_kind, scope_id, parent_grant_id, created_at, updated_at) VALUES(?, ?, 'human', ?, 'system', '', ?, 'organization', ?, '', ?, ?)`, []any{grantID, organizationID, humanID, CapabilityOrganizationAdmin, organizationID, stamp, stamp}},
		{`INSERT INTO agents(id, name, description, driver, created_at, updated_at) VALUES(?, 'legacy-agent', 'legacy', 'native', ?, ?)`, []any{agentID, stamp, stamp}},
		{`INSERT INTO agent_create_requests(request_id, agent_id, payload_fingerprint) VALUES(?, ?, ?)`, []any{requestID, agentID, legacyFingerprint[:]}},
		{`INSERT INTO audit_events(sequence, id, organization_id, actor_kind, actor_id, action, target_kind, target_id, request_id, outcome, reason_code, occurred_at, context_kind, context_id) VALUES(41, ?, ?, 'system', '', 'organization.bootstrap', 'organization', ?, '', 'committed', '', ?, '', '')`, []any{auditID, organizationID, organizationID, stamp}},
	}
	for _, statement := range statements {
		if _, err := database.Exec(statement.query, statement.args...); err != nil {
			database.Close()
			t.Fatal(err)
		}
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	current, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var actorKind, actorID string
	var storedFingerprint []byte
	if err := current.db.QueryRow(`SELECT actor_kind, actor_id, payload_fingerprint FROM agent_create_requests WHERE request_id = ?`, requestID).Scan(&actorKind, &actorID, &storedFingerprint); err != nil {
		current.Close()
		t.Fatal(err)
	}
	if actorKind != "system" || actorID != "" || string(storedFingerprint) != string(legacyFingerprint[:]) {
		current.Close()
		t.Fatalf("legacy receipt = %q/%q/%x", actorKind, actorID, storedFingerprint)
	}
	owner := Principal{Kind: "human", ID: humanID, OrganizationID: organizationID}
	if _, err := current.CreateAgent(context.Background(), CreateAgentParams{
		RequestID: requestID, Actor: owner, Name: "legacy-agent", Description: "legacy", Driver: "native", Now: now.Add(time.Second),
	}); !errors.Is(err, ErrAgentRequestConflict) {
		current.Close()
		t.Fatalf("legacy receipt replay error = %v", err)
	}
	computerID := uuid.NewString()
	tx, err := current.db.BeginTx(context.Background(), nil)
	if err != nil {
		current.Close()
		t.Fatal(err)
	}
	if err := appendAuditEvent(context.Background(), tx, AppendAuditParams{
		OrganizationID: organizationID, Actor: owner, Action: AuditAgentPlace,
		TargetKind: "agent", TargetID: agentID, ContextKind: "computer", ContextID: computerID,
		RequestID: uuid.NewString(), Outcome: "committed", Now: now.Add(2 * time.Second),
	}); err != nil {
		tx.Rollback()
		current.Close()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		current.Close()
		t.Fatal(err)
	}
	events, err := current.ListAuditEvents(context.Background(), ListAuditEventsParams{OrganizationID: organizationID, Limit: 100})
	if err != nil {
		current.Close()
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Sequence != 41 || events[0].ID != auditID || events[1].Sequence != 42 || events[1].ContextKind != "computer" {
		current.Close()
		t.Fatalf("migrated audit events = %+v", events)
	}
	if err := current.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	restartedEvents, err := restarted.ListAuditEvents(context.Background(), ListAuditEventsParams{OrganizationID: organizationID, Limit: 100})
	if err != nil || len(restartedEvents) != 2 || restartedEvents[1].Sequence != 42 {
		t.Fatalf("restart audit events = %+v, %v", restartedEvents, err)
	}
}

func issueAgentAuthorityGrant(t *testing.T, database *Store, owner, subject Principal, capability Capability, scope Scope, parentID string, now time.Time) Grant {
	t.Helper()
	grant, err := database.IssueGrant(context.Background(), IssueGrantParams{
		RequestID: uuid.NewString(), Actor: owner, Subject: subject, Capability: capability,
		Scope: scope, ParentGrantID: parentID, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return grant
}

func tableCount(t *testing.T, database *Store, table string) int {
	t.Helper()
	var count int
	if err := database.db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func assertAuthorityAudit(t *testing.T, database *Store, organizationID, action, targetKind, targetID, contextKind, contextID, outcome, reason string) {
	t.Helper()
	events, err := database.ListAuditEvents(context.Background(), ListAuditEventsParams{OrganizationID: organizationID, Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	for index := len(events) - 1; index >= 0; index-- {
		event := events[index]
		if event.Action == action && event.TargetKind == targetKind && event.TargetID == targetID &&
			event.ContextKind == contextKind && event.ContextID == contextID && event.Outcome == outcome && event.ReasonCode == reason {
			return
		}
	}
	t.Fatalf("audit not found: action=%q target=%q/%q context=%q/%q outcome=%q reason=%q", action, targetKind, targetID, contextKind, contextID, outcome, reason)
}
