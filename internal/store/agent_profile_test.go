package store

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestAgentProfileRevisionReplayAndPlacementReconciliation(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)
	bootstrap, err := database.EnsureAuthority(context.Background(), "profile-owner-credential-abcdefghijklmnopqrstuvwxyz", now)
	if err != nil {
		t.Fatal(err)
	}
	owner := Principal{Kind: PrincipalHuman, ID: bootstrap.Human.ID, OrganizationID: bootstrap.Organization.ID}
	agent, err := database.CreateAgent(context.Background(), testCreateAgentParams(owner, "profile-agent", now.Add(time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	if agent.Profile.Revision != 1 || agent.Profile.AgentID != agent.ID {
		t.Fatalf("created agent profile = %+v", agent.Profile)
	}
	configureTestRuntimeSpec(t, database, owner, agent.ID, now.Add(2*time.Second))

	registrationKey := "profile-computer-key-abcdefghijklmnopqrstuvwxyz"
	computer := pairTestComputer(t, database, owner, registrationKey, testCapabilityInventory("test", true), now.Add(2*time.Second))
	bindTestRuntimeCredential(t, database, agent.ID, computer.ID, "cred_unbound_"+agent.ID, "openai", now.Add(2*time.Second))
	placed, err := database.SetAgentPlacement(context.Background(), SetAgentPlacementParams{
		RequestID: uuid.NewString(), Actor: owner, AgentID: agent.ID, ComputerID: computer.ID, Now: now.Add(3 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if placed.AgentProfileRevision != 1 || placed.DesiredRevision != 1 {
		t.Fatalf("initial placement = %+v", placed)
	}

	request := UpdateAgentProfileParams{
		RequestID: uuid.NewString(), Actor: owner, AgentID: agent.ID, ExpectedRevision: 1,
		DisplayName: "Profile Agent v2", Role: "owner", Mission: "Test immutable profile revisions",
		Instructions: "Never infer authority from role.", Now: now.Add(4 * time.Second),
	}
	updated, err := database.UpdateAgentProfile(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Profile.Revision != 2 || updated.Profile.Role != "owner" {
		t.Fatalf("updated profile = %+v", updated.Profile)
	}
	reconciled, err := database.GetAgentPlacement(context.Background(), agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reconciled.AgentProfileRevision != 2 || reconciled.DesiredRevision != 2 || reconciled.State != "pending" {
		t.Fatalf("reconciled placement = %+v", reconciled)
	}
	if _, err := database.AcknowledgeAgentPlacement(context.Background(), AcknowledgePlacementParams{
		ComputerID: computer.ID, RegistrationKey: registrationKey, AgentID: agent.ID,
		DesiredRevision: placed.DesiredRevision, State: "ready", Now: now.Add(5 * time.Second),
	}); !errors.Is(err, ErrPlacementStale) {
		t.Fatalf("old desired revision acknowledgement error = %v", err)
	}

	replayed, err := database.UpdateAgentProfile(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Profile.Revision != 2 || replayed.Profile.DisplayName != request.DisplayName {
		t.Fatalf("profile replay = %+v", replayed.Profile)
	}
	request.Mission = "Conflicting payload"
	if _, err := database.UpdateAgentProfile(context.Background(), request); !errors.Is(err, ErrAgentRequestConflict) {
		t.Fatalf("changed replay error = %v", err)
	}
	if got := tableCount(t, database, "agent_profiles"); got != 2 {
		t.Fatalf("profile revision count = %d", got)
	}
	currentPlacement, err := database.GetAgentPlacement(context.Background(), agent.ID)
	if err != nil || currentPlacement.DesiredRevision != 2 {
		t.Fatalf("placement after replay = %+v, %v", currentPlacement, err)
	}

	roleActor := Principal{Kind: PrincipalAgent, ID: agent.ID, OrganizationID: owner.OrganizationID}
	if _, err := database.CreateAgent(context.Background(), testCreateAgentParams(roleActor, "role-cannot-create", now.Add(6*time.Second))); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("owner role granted create authority: %v", err)
	}
}

func TestAgentProfileConcurrentExpectedRevisionHasOneWinner(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 7, 24, 11, 0, 0, 0, time.UTC)
	bootstrap, err := database.EnsureAuthority(context.Background(), "profile-concurrency-owner-credential-abcdefghijklmnopqrstuvwxyz", now)
	if err != nil {
		t.Fatal(err)
	}
	owner := Principal{Kind: PrincipalHuman, ID: bootstrap.Human.ID, OrganizationID: bootstrap.Organization.ID}
	agent, err := database.CreateAgent(context.Background(), testCreateAgentParams(owner, "profile-concurrent", now.Add(time.Second)))
	if err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	errorsByRequest := make(chan error, 2)
	var wait sync.WaitGroup
	for index := range 2 {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			_, err := database.UpdateAgentProfile(context.Background(), UpdateAgentProfileParams{
				RequestID: uuid.NewString(), Actor: owner, AgentID: agent.ID, ExpectedRevision: 1,
				DisplayName: "Concurrent Agent", Role: "worker", Mission: "Concurrent mission",
				Instructions: string(rune('A' + index)), Now: now.Add(2 * time.Second),
			})
			errorsByRequest <- err
		}(index)
	}
	close(start)
	wait.Wait()
	close(errorsByRequest)

	var successes, conflicts int
	for err := range errorsByRequest {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrAgentRevisionConflict):
			conflicts++
		default:
			t.Fatalf("concurrent update error = %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent outcomes: successes=%d conflicts=%d", successes, conflicts)
	}
	if got := tableCount(t, database, "agent_profiles"); got != 2 {
		t.Fatalf("profile revision count = %d", got)
	}
}
