package enginefactory

import (
	"context"
	"errors"
	"os/exec"
	"testing"
	"time"

	agentv1 "github.com/abcdlsj/sumi/gen/go/sumi/agent/v1"
	computerv1 "github.com/abcdlsj/sumi/gen/go/sumi/computer/v1"
	computerruntime "github.com/abcdlsj/sumi/internal/computer/runtime"
	computerstate "github.com/abcdlsj/sumi/internal/computer/state"
	"github.com/abcdlsj/sumi/internal/credential"
	"github.com/google/uuid"
)

func TestHealthyInventoryEnginesCanBeOpened(t *testing.T) {
	state, err := computerstate.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	facility := &factoryFacility{secrets: make(map[string][]byte)}
	manager, err := credential.NewManager(context.Background(), state, facility, nil, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	executable, err := exec.LookPath("sh")
	if err != nil {
		t.Fatal(err)
	}
	factory, err := New(Config{
		Discovery: Discovery{CodexPath: executable, ClaudePath: executable}, CredentialManager: manager,
		State: state, ServerURL: "http://127.0.0.1:1",
	})
	if err != nil {
		t.Fatal(err)
	}
	inventory, err := factory.discovery.Inventory(manager)
	if err != nil {
		t.Fatal(err)
	}
	if inventory.GetCredentialDelivery().GetHealth() != computerv1.CapabilityHealth_CAPABILITY_HEALTH_HEALTHY || len(inventory.GetEngines()) != 3 {
		t.Fatalf("inventory = %+v", inventory)
	}

	agentID, computerID := uuid.NewString(), uuid.NewString()
	for _, test := range []struct {
		kind       agentv1.EngineKind
		protocol   agentv1.ProviderProtocol
		credential string
	}{
		{agentv1.EngineKind_ENGINE_KIND_BUILTIN, agentv1.ProviderProtocol_PROVIDER_PROTOCOL_OPENAI_RESPONSES, "openai"},
		{agentv1.EngineKind_ENGINE_KIND_CODEX_ADAPTER, agentv1.ProviderProtocol_PROVIDER_PROTOCOL_UNSPECIFIED, "codex_adapter"},
		{agentv1.EngineKind_ENGINE_KIND_CLAUDE_ADAPTER, agentv1.ProviderProtocol_PROVIDER_PROTOCOL_UNSPECIFIED, "claude_adapter"},
	} {
		t.Run(test.kind.String(), func(t *testing.T) {
			handle := "cred_factory_" + uuid.NewString()
			if err := facility.Put(context.Background(), handle, []byte("provider-secret")); err != nil {
				t.Fatal(err)
			}
			if err := state.SaveCredentialBinding(context.Background(), computerstate.CredentialBinding{
				Handle: handle, DeliveryID: uuid.NewString(), AgentID: agentID, ComputerID: computerID,
				CredentialKind: test.credential, KeyID: manager.Key().KeyID, CreatedAt: time.Now(),
			}); err != nil {
				t.Fatal(err)
			}
			slot := factorySlot(t, agentID, computerID, handle, test.kind, test.protocol)
			if err := factory.Validate(slot); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
			opened, err := factory.Open(context.Background(), slot)
			if err != nil {
				t.Fatalf("Open() error = %v", err)
			}
			if err := opened.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestFactoryFailsClosedForUndeclaredBoundaries(t *testing.T) {
	if _, err := New(Config{ServerURL: "http://127.0.0.1"}); err == nil || err.Error() != "engine factory computer state is required" {
		t.Fatalf("missing State error = %v", err)
	}
	state, err := computerstate.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if _, err := New(Config{State: state}); err == nil || err.Error() != "engine factory Server URL is required" {
		t.Fatalf("missing Server URL error = %v", err)
	}
	discovery := Discovery{}
	inventory, err := discovery.Inventory(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, engine := range inventory.GetEngines() {
		if engine.GetHealth() != computerv1.CapabilityHealth_CAPABILITY_HEALTH_UNAVAILABLE {
			t.Fatalf("engine without credential manager advertised healthy: %+v", engine)
		}
	}
}

func factorySlot(t *testing.T, agentID, computerID, handle string, kind agentv1.EngineKind, protocol agentv1.ProviderProtocol) computerruntime.SlotConfig {
	t.Helper()
	spec := &agentv1.AgentRuntimeSpec{
		AgentId: agentID, Revision: 1, Engine: kind, ProviderProtocol: protocol,
		CredentialBindingHandle: handle,
		SandboxProvider:         agentv1.RuntimeSandboxProvider_RUNTIME_SANDBOX_PROVIDER_TRUSTED_LOCAL,
		MaxRunDurationSeconds:   30, MaxOutputBytes: 1 << 20, ToolPolicy: &agentv1.RuntimeToolPolicy{},
	}
	if kind == agentv1.EngineKind_ENGINE_KIND_BUILTIN {
		spec.ProviderEndpoint = "https://provider.invalid/v1"
		spec.Model = "test-model"
	}
	return computerruntime.SlotConfig{
		AgentID: agentID, ComputerID: computerID, PlacementDesiredRevision: 1,
		AgentProfile: &agentv1.AgentProfile{
			AgentId: agentID, Revision: 1, DisplayName: "Factory Agent", Role: "worker", Mission: "Verify factory boundaries",
		},
		RuntimeSpec: spec, Workspace: t.TempDir(), Home: t.TempDir(), Temp: t.TempDir(), Cache: t.TempDir(),
	}
}

type factoryFacility struct {
	secrets map[string][]byte
}

func (*factoryFacility) Kind() string { return "linux_secret_service" }

func (facility *factoryFacility) Put(_ context.Context, handle string, secret []byte) error {
	facility.secrets[handle] = append([]byte(nil), secret...)
	return nil
}

func (facility *factoryFacility) Get(_ context.Context, handle string) ([]byte, error) {
	secret, found := facility.secrets[handle]
	if !found {
		return nil, errors.New("secret not found")
	}
	return append([]byte(nil), secret...), nil
}

func (facility *factoryFacility) Delete(_ context.Context, handle string) error {
	delete(facility.secrets, handle)
	return nil
}
