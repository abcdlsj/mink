package store

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	agentapp "github.com/abcdlsj/sumi/internal/agent/application"
	"github.com/google/uuid"
)

func TestAgentRuntimeSpecRequiredVersionedAndReconcilesPlacement(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	bootstrap, err := database.EnsureAuthority(context.Background(), "runtime-spec-owner-credential-abcdefghijklmnopqrstuvwxyz", now)
	if err != nil {
		t.Fatal(err)
	}
	owner := Principal{Kind: PrincipalHuman, ID: bootstrap.Human.ID, OrganizationID: bootstrap.Organization.ID}
	agent, err := database.CreateAgent(context.Background(), testCreateAgentParams(owner, "runtime-spec-agent", now.Add(time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	computer := pairTestComputer(t, database, owner, "runtime-spec-computer-key-abcdefghijklmnopqrstuvwxyz", testCapabilityInventory("test", true), now.Add(2*time.Second))
	if _, err := database.GetAgentRuntimeSpec(context.Background(), agent.ID); !errors.Is(err, ErrAgentRuntimeSpecMissing) {
		t.Fatalf("missing runtime spec error = %v", err)
	}
	if _, err := database.SetAgentPlacement(context.Background(), SetAgentPlacementParams{
		RequestID: uuid.NewString(), Actor: owner, AgentID: agent.ID, ComputerID: computer.ID, Now: now.Add(3 * time.Second),
	}); !errors.Is(err, ErrAgentRuntimeSpecMissing) {
		t.Fatalf("placement without runtime spec error = %v", err)
	}

	firstRequest := UpdateAgentRuntimeSpecParams{
		RequestID: uuid.NewString(), Actor: owner, AgentID: agent.ID,
		Engine: agentapp.EngineCodexAdapter, CredentialBindingHandle: "codex-binding-handle",
		SandboxProvider: "trusted_local", MaxRunDuration: 3 * time.Minute, MaxOutputBytes: 2 << 20,
		ToolPolicy: agentapp.ToolPolicy{Message: true, Artifact: true}, Now: now.Add(4 * time.Second),
	}
	first, err := database.UpdateAgentRuntimeSpec(context.Background(), firstRequest)
	if err != nil {
		t.Fatal(err)
	}
	if first.Revision != 1 || first.Engine != agentapp.EngineCodexAdapter {
		t.Fatalf("first runtime spec = %+v", first)
	}
	bindTestRuntimeCredential(t, database, agent.ID, computer.ID, first.CredentialBindingHandle, "codex_adapter", now.Add(4*time.Second))
	placed, err := database.SetAgentPlacement(context.Background(), SetAgentPlacementParams{
		RequestID: uuid.NewString(), Actor: owner, AgentID: agent.ID, ComputerID: computer.ID, Now: now.Add(5 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if placed.RuntimeSpec.Revision != 1 || placed.RuntimeSpec.Engine != agentapp.EngineCodexAdapter {
		t.Fatalf("placement runtime spec = %+v", placed.RuntimeSpec)
	}

	secondRequest := firstRequest
	secondRequest.RequestID = uuid.NewString()
	secondRequest.ExpectedRevision = 1
	secondRequest.Engine = agentapp.EngineClaudeAdapter
	secondRequest.CredentialBindingHandle = "claude-binding-handle"
	secondRequest.Now = now.Add(6 * time.Second)
	second, err := database.UpdateAgentRuntimeSpec(context.Background(), secondRequest)
	if err != nil {
		t.Fatal(err)
	}
	if second.Revision != 2 || second.Engine != agentapp.EngineClaudeAdapter {
		t.Fatalf("second runtime spec = %+v", second)
	}
	reconciled, err := database.GetAgentPlacement(context.Background(), agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reconciled.RuntimeSpec.Revision != 2 || reconciled.DesiredRevision != placed.DesiredRevision+1 || reconciled.State != "pending" {
		t.Fatalf("reconciled placement = %+v", reconciled)
	}

	replayed, err := database.UpdateAgentRuntimeSpec(context.Background(), secondRequest)
	if err != nil || replayed.Revision != 2 {
		t.Fatalf("runtime spec replay = %+v, %v", replayed, err)
	}
	secondRequest.Model = "conflict"
	if _, err := database.UpdateAgentRuntimeSpec(context.Background(), secondRequest); !errors.Is(err, ErrAgentRequestConflict) {
		t.Fatalf("changed runtime spec replay error = %v", err)
	}
	if got := tableCount(t, database, "agent_runtime_specs"); got != 2 {
		t.Fatalf("runtime spec count = %d", got)
	}
}

func TestAgentRuntimeSpecConcurrentExpectedRevisionHasOneWinner(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 7, 24, 13, 0, 0, 0, time.UTC)
	bootstrap, err := database.EnsureAuthority(context.Background(), "runtime-spec-concurrency-owner-credential-abcdefghijklmnopqrstuvwxyz", now)
	if err != nil {
		t.Fatal(err)
	}
	owner := Principal{Kind: PrincipalHuman, ID: bootstrap.Human.ID, OrganizationID: bootstrap.Organization.ID}
	agent, err := database.CreateAgent(context.Background(), testCreateAgentParams(owner, "runtime-spec-concurrent", now.Add(time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	configureTestRuntimeSpec(t, database, owner, agent.ID, now.Add(2*time.Second))

	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for index := range 2 {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			_, err := database.UpdateAgentRuntimeSpec(context.Background(), UpdateAgentRuntimeSpecParams{
				RequestID: uuid.NewString(), Actor: owner, AgentID: agent.ID, ExpectedRevision: 1,
				Engine: agentapp.EngineCodexAdapter, CredentialBindingHandle: "binding-" + string(rune('a'+index)),
				SandboxProvider: "trusted_local", MaxRunDuration: time.Minute, MaxOutputBytes: 1 << 20,
				ToolPolicy: agentapp.ToolPolicy{Message: true}, Now: now.Add(3 * time.Second),
			})
			results <- err
		}(index)
	}
	close(start)
	wait.Wait()
	close(results)

	var successes, conflicts int
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrAgentRuntimeSpecRevisionConflict):
			conflicts++
		default:
			t.Fatalf("runtime spec update error = %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent outcomes: successes=%d conflicts=%d", successes, conflicts)
	}
}
