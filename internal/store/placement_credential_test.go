package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	agentapp "github.com/abcdlsj/sumi/internal/agent/application"
	placementapp "github.com/abcdlsj/sumi/internal/placement/application"
	"github.com/google/uuid"
)

func TestPlacementRejectsCredentialBindingMismatchWithoutFacts(t *testing.T) {
	for _, test := range []struct {
		name       string
		binding    bool
		wrongAgent bool
		wrongHost  bool
		kind       string
	}{
		{name: "missing"},
		{name: "wrong agent", binding: true, wrongAgent: true, kind: "openai"},
		{name: "wrong computer", binding: true, wrongHost: true, kind: "openai"},
		{name: "wrong kind", binding: true, kind: "anthropic"},
	} {
		t.Run(test.name, func(t *testing.T) {
			database, owner, agentID, computerID, now := openPlacementCredentialFixture(t)
			defer database.Close()
			handle := "cred_binding_" + uuid.NewString()
			if test.binding {
				bindingAgentID := agentID
				if test.wrongAgent {
					other, err := database.CreateAgent(context.Background(), testCreateAgentParams(owner, "binding-owner", now.Add(time.Second)))
					if err != nil {
						t.Fatal(err)
					}
					bindingAgentID = other.ID
				}
				bindingComputerID := computerID
				if test.wrongHost {
					bindingComputerID = pairTestComputer(t, database, owner, "other-placement-computer-key", testCapabilityInventory("test", true), now).ID
				}
				bindTestRuntimeCredential(t, database, bindingAgentID, bindingComputerID, handle, test.kind, now)
			}
			configurePlacementCredentialRuntime(t, database, owner, agentID, handle, agentapp.EngineBuiltin, agentapp.ProviderOpenAIResponses, now)
			_, err := database.SetAgentPlacement(context.Background(), SetAgentPlacementParams{
				RequestID: uuid.NewString(), Actor: owner, AgentID: agentID, ComputerID: computerID, Now: now.Add(time.Second),
			})
			if !errors.Is(err, placementapp.ErrCredentialBindingInvalid) {
				t.Fatalf("SetAgentPlacement() error = %v", err)
			}
			if _, err := database.GetAgentPlacement(context.Background(), agentID); !errors.Is(err, ErrPlacementNotFound) {
				t.Fatalf("rejected placement fact error = %v", err)
			}
			if got := tableCount(t, database, "agent_placement_requests"); got != 0 {
				t.Fatalf("rejected placement persisted %d receipts", got)
			}
		})
	}
}

func TestPlacementAcceptsCredentialKindRequiredByEngine(t *testing.T) {
	for _, test := range []struct {
		name     string
		engine   agentapp.EngineKind
		protocol agentapp.ProviderProtocol
		kind     string
	}{
		{name: "builtin OpenAI", engine: agentapp.EngineBuiltin, protocol: agentapp.ProviderOpenAIResponses, kind: "openai"},
		{name: "builtin Anthropic", engine: agentapp.EngineBuiltin, protocol: agentapp.ProviderAnthropicMessages, kind: "anthropic"},
		{name: "Codex adapter", engine: agentapp.EngineCodexAdapter, kind: "codex_adapter"},
		{name: "Claude adapter", engine: agentapp.EngineClaudeAdapter, kind: "claude_adapter"},
	} {
		t.Run(test.name, func(t *testing.T) {
			database, owner, agentID, computerID, now := openPlacementCredentialFixture(t)
			defer database.Close()
			handle := "cred_binding_" + uuid.NewString()
			bindTestRuntimeCredential(t, database, agentID, computerID, handle, test.kind, now)
			configurePlacementCredentialRuntime(t, database, owner, agentID, handle, test.engine, test.protocol, now)
			placement, err := database.SetAgentPlacement(context.Background(), SetAgentPlacementParams{
				RequestID: uuid.NewString(), Actor: owner, AgentID: agentID, ComputerID: computerID, Now: now.Add(time.Second),
			})
			if err != nil {
				t.Fatal(err)
			}
			if placement.AgentID != agentID || placement.ComputerID != computerID || placement.State != "pending" {
				t.Fatalf("placement = %+v", placement)
			}
		})
	}
}

func openPlacementCredentialFixture(t *testing.T) (*Store, Principal, string, string, time.Time) {
	t.Helper()
	database, err := Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 24, 16, 0, 0, 0, time.UTC)
	bootstrap, err := database.EnsureAuthority(context.Background(), "placement-credential-owner-key-abcdefghijklmnopqrstuvwxyz", now)
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	owner := Principal{Kind: PrincipalHuman, ID: bootstrap.Human.ID, OrganizationID: bootstrap.Organization.ID}
	agent, err := database.CreateAgent(context.Background(), testCreateAgentParams(owner, "credential-placement", now))
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	computer := pairTestComputer(t, database, owner, "placement-credential-computer-key", testCapabilityInventory("test", true), now)
	return database, owner, agent.ID, computer.ID, now
}

func configurePlacementCredentialRuntime(
	t *testing.T,
	database *Store,
	owner Principal,
	agentID, handle string,
	engine agentapp.EngineKind,
	protocol agentapp.ProviderProtocol,
	now time.Time,
) {
	t.Helper()
	endpoint, model := "", ""
	if engine == agentapp.EngineBuiltin {
		endpoint, model = "https://provider.invalid/v1", "test-model"
	}
	if _, err := database.UpdateAgentRuntimeSpec(context.Background(), UpdateAgentRuntimeSpecParams{
		RequestID: uuid.NewString(), Actor: owner, AgentID: agentID,
		Engine: engine, ProviderProtocol: protocol, ProviderEndpoint: endpoint, Model: model,
		CredentialBindingHandle: handle, SandboxProvider: "trusted_local",
		MaxRunDuration: time.Minute, MaxOutputBytes: 1 << 20, Now: now,
	}); err != nil {
		t.Fatal(err)
	}
}
