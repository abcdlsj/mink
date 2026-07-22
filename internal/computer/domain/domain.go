package domain

type OperatingSystem string

const (
	OperatingSystemMacOS OperatingSystem = "macos"
	OperatingSystemLinux OperatingSystem = "linux"
)

type Architecture string

const (
	ArchitectureARM64 Architecture = "arm64"
	ArchitectureAMD64 Architecture = "amd64"
)

type SandboxCapability struct {
	Provider              string
	Isolation             string
	WorkspaceAccess       string
	ProcessControl        string
	FilesystemIsolation   string
	NetworkIsolation      string
	SecretMaterialization string
	DaemonCrashCleanup    string
}

func UnknownSandboxCapability() SandboxCapability {
	return SandboxCapability{
		Provider: "unknown", Isolation: "unknown", WorkspaceAccess: "unknown", ProcessControl: "unknown",
		FilesystemIsolation: "unknown", NetworkIsolation: "unknown", SecretMaterialization: "unknown",
		DaemonCrashCleanup: "unknown",
	}
}

func TrustedLocalSandboxCapability() SandboxCapability {
	return SandboxCapability{
		Provider: "trusted_local", Isolation: "trusted_local", WorkspaceAccess: "direct_read_write",
		ProcessControl: "context_process_group", FilesystemIsolation: "none", NetworkIsolation: "none",
		SecretMaterialization: "ephemeral_environment", DaemonCrashCleanup: "none",
	}
}

func (capability SandboxCapability) Valid() bool {
	return capability == UnknownSandboxCapability() || capability == TrustedLocalSandboxCapability()
}
