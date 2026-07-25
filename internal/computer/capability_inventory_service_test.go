package computer

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	agentv1 "github.com/abcdlsj/sumi/gen/go/sumi/agent/v1"
	computerv1 "github.com/abcdlsj/sumi/gen/go/sumi/computer/v1"
	authorityapp "github.com/abcdlsj/sumi/internal/authority/application"
	"github.com/abcdlsj/sumi/internal/authority/localauth"
	"github.com/abcdlsj/sumi/internal/store"
	"github.com/google/uuid"
)

func TestCapabilityInventoryRoundTripAndInvalidRequestIsZeroWrite(t *testing.T) {
	database, err := store.Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	key := "inventory-service-registration-key"
	password := "inventory-password-1234567890"
	digest, err := localauth.HashPassword(rand.Reader, password, localauth.DefaultPasswordParameters())
	if err != nil {
		t.Fatal(err)
	}
	sessionToken := "abcdefghijklmnopqrstuvwxyz-ABCDEFGHIJKLMNOP"
	bootstrap, err := database.RegisterFirstOwner(context.Background(), authorityapp.RegisterFirstOwnerCommand{
		RequestID: uuid.NewString(), Name: "Owner",
		Identity:         authorityapp.AuthenticationIdentity{Provider: "local", Subject: "owner"},
		Password:         digest,
		SessionToken:     sessionToken,
		Now:              now,
		SessionExpiresAt: now.Add(12 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	owner := store.Principal{Kind: "human", ID: bootstrap.Human.ID, OrganizationID: bootstrap.Organization.ID}
	pairingToken := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	if _, err := database.CreateComputerPairing(context.Background(), store.CreateComputerPairingParams{
		RequestID: uuid.NewString(), Actor: owner, Token: pairingToken, ExpiresAt: now.Add(time.Minute), Now: now,
	}); err != nil {
		t.Fatal(err)
	}
	service := New(database)
	service.now = func() time.Time { return now }
	paired, err := service.RegisterComputer(context.Background(), connect.NewRequest(&computerv1.RegisterComputerRequest{
		RequestId: uuid.NewString(), PairingToken: pairingToken,
		RegistrationKey: key, Name: "Inventory service host",
		Os: computerv1.OperatingSystem_OPERATING_SYSTEM_LINUX, Arch: computerv1.Architecture_ARCHITECTURE_AMD64,
		CapabilityInventory: testInventoryDeclaration("initial"),
	}))
	if err != nil {
		t.Fatal(err)
	}
	computer := paired.Msg.GetComputer()
	if computer.GetCapabilityInventory().GetRevision() != 1 ||
		computer.GetCapabilityInventory().GetEngines()[0].GetVersion() != "initial" {
		t.Fatalf("paired inventory = %+v", computer.GetCapabilityInventory())
	}
	service.now = func() time.Time { return now.Add(time.Minute) }
	heartbeat, err := service.HeartbeatComputer(context.Background(), connect.NewRequest(&computerv1.HeartbeatComputerRequest{
		ComputerId: computer.GetId(), RegistrationKey: key, CapabilityInventory: testInventoryDeclaration("heartbeat"),
	}))
	if err != nil {
		t.Fatal(err)
	}
	if heartbeat.Msg.GetComputer().GetCapabilityInventory().GetRevision() != 2 ||
		heartbeat.Msg.GetComputer().GetCapabilityInventory().GetSandboxes()[0].GetFilesystemIsolation() != computerv1.SandboxFilesystemIsolation_SANDBOX_FILESYSTEM_ISOLATION_NONE {
		t.Fatalf("heartbeat inventory = %+v", heartbeat.Msg.GetComputer().GetCapabilityInventory())
	}
	listed, err := service.ListComputers(context.Background(), connect.NewRequest(&computerv1.ListComputersRequest{}))
	if err != nil || len(listed.Msg.GetComputers()) != 1 || listed.Msg.GetComputers()[0].GetCapabilityInventory().GetRevision() != 2 {
		t.Fatalf("listed computers = %+v, %v", listed.Msg.GetComputers(), err)
	}

	invalid := testInventoryDeclaration("invalid")
	invalid.Engines = append(invalid.Engines, invalid.Engines[0])
	service.now = func() time.Time { return now.Add(2 * time.Minute) }
	_, err = service.HeartbeatComputer(context.Background(), connect.NewRequest(&computerv1.HeartbeatComputerRequest{
		ComputerId: computer.GetId(), RegistrationKey: key, CapabilityInventory: invalid,
	}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("invalid inventory error = %v", err)
	}
	current, err := database.GetComputer(context.Background(), computer.GetId())
	if err != nil {
		t.Fatal(err)
	}
	if current.CapabilityInventory.Revision != 2 || !current.LastSeenAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("invalid request mutated current fact: %+v", current)
	}
}

func testInventoryDeclaration(version string) *computerv1.CapabilityInventoryDeclaration {
	return &computerv1.CapabilityInventoryDeclaration{
		Engines: []*computerv1.EngineCapability{{
			Engine: agentv1.EngineKind_ENGINE_KIND_BUILTIN, Version: version, ProtocolVersion: 1,
			SupportsToolCalls: true, SupportsCancel: true,
			ProviderProtocols: []agentv1.ProviderProtocol{agentv1.ProviderProtocol_PROVIDER_PROTOCOL_OPENAI_RESPONSES},
			Health:            computerv1.CapabilityHealth_CAPABILITY_HEALTH_HEALTHY,
		}},
		Sandboxes: []*computerv1.SandboxCapability{{
			Provider:              computerv1.SandboxProvider_SANDBOX_PROVIDER_TRUSTED_LOCAL,
			Isolation:             computerv1.SandboxIsolation_SANDBOX_ISOLATION_TRUSTED_LOCAL,
			WorkspaceAccess:       computerv1.SandboxWorkspaceAccess_SANDBOX_WORKSPACE_ACCESS_DIRECT_READ_WRITE,
			ProcessControl:        computerv1.SandboxProcessControl_SANDBOX_PROCESS_CONTROL_CONTEXT_PROCESS_GROUP,
			FilesystemIsolation:   computerv1.SandboxFilesystemIsolation_SANDBOX_FILESYSTEM_ISOLATION_NONE,
			NetworkIsolation:      computerv1.SandboxNetworkIsolation_SANDBOX_NETWORK_ISOLATION_NONE,
			SecretMaterialization: computerv1.SandboxSecretMaterialization_SANDBOX_SECRET_MATERIALIZATION_EPHEMERAL_ENVIRONMENT,
			DaemonCrashCleanup:    computerv1.SandboxDaemonCrashCleanup_SANDBOX_DAEMON_CRASH_CLEANUP_NONE,
		}},
		CredentialDelivery: &computerv1.CredentialDeliveryCapability{Health: computerv1.CapabilityHealth_CAPABILITY_HEALTH_UNAVAILABLE},
	}
}
