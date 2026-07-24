package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	agentapp "github.com/abcdlsj/sumi/internal/agent/application"
	"github.com/google/uuid"
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

	deniedCreate := testCreateAgentParams(peer, "denied-agent", now.Add(2*time.Second))
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
	createParams := testCreateAgentParams(peer, "delegated-agent", now.Add(4*time.Second))
	agent, err := database.CreateAgent(context.Background(), createParams)
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	assertAuthorityAudit(t, database, owner.OrganizationID, AuditAgentCreate, "agent", agent.ID, "", "", "committed", "")
	if _, err := database.CreateAgent(context.Background(), CreateAgentParams{
		RequestID: createParams.RequestID, Actor: owner, Handle: createParams.Handle,
		DisplayName: createParams.DisplayName, Role: createParams.Role, Mission: createParams.Mission,
		Instructions: createParams.Instructions, Now: now.Add(5 * time.Second),
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
	computer := pairTestComputer(t, database, owner, computerKey, testCapabilityInventory("test", true), now.Add(7*time.Second))
	bindTestRuntimeCredential(t, database, agent.ID, computer.ID, testCredentialHandle(agent.ID, computer.ID), "openai", now.Add(7*time.Second))
	configureTestRuntimeSpec(t, database, owner, agent.ID, now.Add(7*time.Second))
	placeParams := SetAgentPlacementParams{
		RequestID: uuid.NewString(), Actor: owner, AgentID: agent.ID, ComputerID: computer.ID, Now: now.Add(8 * time.Second),
	}
	placed, err := database.SetAgentPlacement(context.Background(), placeParams)
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	assertAuthorityAudit(t, database, owner.OrganizationID, AuditAgentPlace, "agent", agent.ID, "computer", computer.ID, "committed", "")
	ready, err := database.AcknowledgeAgentPlacement(context.Background(), AcknowledgePlacementParams{
		ComputerID: computer.ID, RegistrationKey: computerKey, AgentID: agent.ID,
		DesiredRevision: placed.DesiredRevision, State: "ready", Now: now.Add(9 * time.Second),
	})
	if err != nil || ready.State != "ready" {
		database.Close()
		t.Fatalf("placement acknowledgement = %+v, %v", ready, err)
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

	secondAgent, err := database.CreateAgent(context.Background(), testCreateAgentParams(owner, "placement-target", now.Add(10*time.Second)))
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	bindTestRuntimeCredential(t, database, secondAgent.ID, computer.ID, testCredentialHandle(secondAgent.ID, computer.ID), "openai", now.Add(10*time.Second))
	configureTestRuntimeSpec(t, database, owner, secondAgent.ID, now.Add(10*time.Second))
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
	if _, err := database.CreateAgent(context.Background(), testCreateAgentParams(peer, "disabled-create", now.Add(18*time.Second))); !errors.Is(err, ErrPermissionDenied) {
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

func testCreateAgentParams(actor Principal, handle string, now time.Time) CreateAgentParams {
	return CreateAgentParams{
		RequestID: uuid.NewString(), Actor: actor, Handle: handle, DisplayName: handle,
		Role: "collaborator", Mission: "Complete assigned work", Now: now,
	}
}

func configureTestRuntimeSpec(t *testing.T, database *Store, actor Principal, agentID string, now time.Time) AgentRuntimeSpec {
	t.Helper()
	bindingHandle := "cred_unbound_" + agentID
	var completedBinding string
	if err := database.db.QueryRow(`SELECT handle FROM credential_bindings WHERE agent_id = ? ORDER BY created_at DESC LIMIT 1`, agentID).Scan(&completedBinding); err == nil {
		bindingHandle = completedBinding
	} else if !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	spec, err := database.UpdateAgentRuntimeSpec(context.Background(), UpdateAgentRuntimeSpecParams{
		RequestID: uuid.NewString(), Actor: actor, AgentID: agentID,
		Engine: agentapp.EngineBuiltin, ProviderProtocol: agentapp.ProviderOpenAIResponses,
		ProviderEndpoint: "https://provider.invalid/v1", Model: "test-model",
		CredentialBindingHandle: bindingHandle, SandboxProvider: "trusted_local",
		MaxRunDuration: 2 * time.Minute, MaxOutputBytes: 1 << 20,
		ToolPolicy: agentapp.ToolPolicy{Message: true, Work: true, Artifact: true, Knowledge: true}, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return spec
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
