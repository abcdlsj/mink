package computer

import (
	"time"

	computerv1 "github.com/abcdlsj/sumi/gen/go/sumi/computer/v1"
	"github.com/abcdlsj/sumi/internal/store"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func computerMessage(computer store.Computer, now time.Time) *computerv1.Computer {
	os := computerv1.OperatingSystem_OPERATING_SYSTEM_UNSPECIFIED
	if computer.OS == "macos" {
		os = computerv1.OperatingSystem_OPERATING_SYSTEM_MACOS
	} else if computer.OS == "linux" {
		os = computerv1.OperatingSystem_OPERATING_SYSTEM_LINUX
	}
	arch := computerv1.Architecture_ARCHITECTURE_UNSPECIFIED
	if computer.Arch == "arm64" {
		arch = computerv1.Architecture_ARCHITECTURE_ARM64
	} else if computer.Arch == "amd64" {
		arch = computerv1.Architecture_ARCHITECTURE_AMD64
	}
	expiresAt := computer.LastSeenAt.Add(connectivityTTL)
	return &computerv1.Computer{
		Id:                         computer.ID,
		Name:                       computer.Name,
		Os:                         os,
		Arch:                       arch,
		CreatedAt:                  timestamppb.New(computer.CreatedAt),
		LastSeenAt:                 timestamppb.New(computer.LastSeenAt),
		Online:                     now.Before(expiresAt),
		ConnectivityExpiresAt:      timestamppb.New(expiresAt),
		SandboxCapability:          sandboxCapabilityMessage(computer.SandboxCapability),
		SandboxDeclarationRevision: computer.SandboxDeclarationRevision,
	}
}

func sandboxCapabilityMessage(value store.SandboxCapability) *computerv1.SandboxCapability {
	if value == store.TrustedLocalSandboxCapability() {
		return trustedLocalSandboxCapabilityMessage()
	}
	return &computerv1.SandboxCapability{}
}

func trustedLocalSandboxCapabilityMessage() *computerv1.SandboxCapability {
	return &computerv1.SandboxCapability{
		Provider:              computerv1.SandboxProvider_SANDBOX_PROVIDER_TRUSTED_LOCAL,
		Isolation:             computerv1.SandboxIsolation_SANDBOX_ISOLATION_TRUSTED_LOCAL,
		WorkspaceAccess:       computerv1.SandboxWorkspaceAccess_SANDBOX_WORKSPACE_ACCESS_DIRECT_READ_WRITE,
		ProcessControl:        computerv1.SandboxProcessControl_SANDBOX_PROCESS_CONTROL_CONTEXT_PROCESS_GROUP,
		FilesystemIsolation:   computerv1.SandboxFilesystemIsolation_SANDBOX_FILESYSTEM_ISOLATION_NONE,
		NetworkIsolation:      computerv1.SandboxNetworkIsolation_SANDBOX_NETWORK_ISOLATION_NONE,
		SecretMaterialization: computerv1.SandboxSecretMaterialization_SANDBOX_SECRET_MATERIALIZATION_EPHEMERAL_ENVIRONMENT,
		DaemonCrashCleanup:    computerv1.SandboxDaemonCrashCleanup_SANDBOX_DAEMON_CRASH_CLEANUP_NONE,
	}
}
