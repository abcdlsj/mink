package computer

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	computerv1 "github.com/abcdlsj/sumi/gen/go/sumi/computer/v1"
	computerdomain "github.com/abcdlsj/sumi/internal/computer/domain"
	"github.com/abcdlsj/sumi/internal/store"
)

func TestComputerSandboxCapabilityRoundTripAndInvalidRequestIsZeroWrite(t *testing.T) {
	database, err := store.Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	key := "sandbox-service-registration-key"
	computer, err := database.RegisterComputer(context.Background(), store.RegisterComputerParams{
		RegistrationKey: key, Name: "Sandbox service host", OS: "linux", Arch: "amd64", Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	service := New(database)
	service.now = func() time.Time { return now.Add(time.Minute) }
	recovered, err := service.RegisterComputer(context.Background(), connect.NewRequest(&computerv1.RegisterComputerRequest{
		RegistrationKey:   key,
		Name:              "Sandbox service host",
		Os:                computerv1.OperatingSystem_OPERATING_SYSTEM_LINUX,
		Arch:              computerv1.Architecture_ARCHITECTURE_AMD64,
		SandboxCapability: trustedLocalSandboxCapabilityMessage(),
	}))
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Msg.GetComputer().GetSandboxDeclarationRevision() != 2 ||
		recovered.Msg.GetComputer().GetSandboxCapability().GetProvider() != computerv1.SandboxProvider_SANDBOX_PROVIDER_TRUSTED_LOCAL {
		t.Fatalf("trusted-local recovery = %+v", recovered.Msg.GetComputer())
	}
	service.now = func() time.Time { return now.Add(2 * time.Minute) }
	heartbeat, err := service.HeartbeatComputer(context.Background(), connect.NewRequest(&computerv1.HeartbeatComputerRequest{
		ComputerId: computer.ID, RegistrationKey: key, SandboxCapability: trustedLocalSandboxCapabilityMessage(),
	}))
	if err != nil {
		t.Fatal(err)
	}
	if heartbeat.Msg.GetComputer().GetSandboxDeclarationRevision() != 3 ||
		heartbeat.Msg.GetComputer().GetSandboxCapability().GetProvider() != computerv1.SandboxProvider_SANDBOX_PROVIDER_TRUSTED_LOCAL {
		t.Fatalf("trusted-local heartbeat = %+v", heartbeat.Msg.GetComputer())
	}
	listed, err := service.ListComputers(context.Background(), connect.NewRequest(&computerv1.ListComputersRequest{}))
	if err != nil || len(listed.Msg.GetComputers()) != 1 || listed.Msg.GetComputers()[0].GetSandboxDeclarationRevision() != 3 {
		t.Fatalf("listed computers = %+v, %v", listed.Msg.GetComputers(), err)
	}

	invalid := trustedLocalSandboxCapabilityMessage()
	invalid.NetworkIsolation = computerv1.SandboxNetworkIsolation_SANDBOX_NETWORK_ISOLATION_UNSPECIFIED
	service.now = func() time.Time { return now.Add(3 * time.Minute) }
	_, err = service.HeartbeatComputer(context.Background(), connect.NewRequest(&computerv1.HeartbeatComputerRequest{
		ComputerId: computer.ID, RegistrationKey: key, SandboxCapability: invalid,
	}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("invalid capability error = %v", err)
	}
	current, err := database.GetComputer(context.Background(), computer.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.SandboxDeclarationRevision != 3 || !current.LastSeenAt.Equal(now.Add(2*time.Minute)) || current.SandboxCapability != computerdomain.TrustedLocalSandboxCapability() {
		t.Fatalf("invalid request mutated current fact: %+v", current)
	}
}
