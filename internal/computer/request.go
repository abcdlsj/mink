package computer

import (
	"encoding/base64"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"connectrpc.com/connect"
	computerv1 "github.com/abcdlsj/sumi/gen/go/sumi/computer/v1"
	computerapp "github.com/abcdlsj/sumi/internal/computer/application"
	computerdomain "github.com/abcdlsj/sumi/internal/computer/domain"
)

func pairingTokenValid(token string) error {
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(decoded) != 32 || base64.RawURLEncoding.EncodeToString(decoded) != token {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("pairing token is invalid"))
	}
	return nil
}

func registerParams(request *computerv1.RegisterComputerRequest, now time.Time) (computerapp.RegistrationCommand, error) {
	if err := registrationKeyValid(request.GetRegistrationKey()); err != nil {
		return computerapp.RegistrationCommand{}, err
	}
	if err := displayNameValid(request.GetName()); err != nil {
		return computerapp.RegistrationCommand{}, err
	}
	os, ok := operatingSystem(request.GetOs())
	if !ok {
		return computerapp.RegistrationCommand{}, connect.NewError(connect.CodeInvalidArgument, errors.New("operating system must be macos or linux"))
	}
	arch, ok := architecture(request.GetArch())
	if !ok {
		return computerapp.RegistrationCommand{}, connect.NewError(connect.CodeInvalidArgument, errors.New("architecture must be arm64 or amd64"))
	}
	capability, err := sandboxCapability(request.GetSandboxCapability())
	if err != nil {
		return computerapp.RegistrationCommand{}, err
	}
	return computerapp.RegistrationCommand{
		RegistrationKey:   request.GetRegistrationKey(),
		Name:              request.GetName(),
		OS:                os,
		Arch:              arch,
		SandboxCapability: capability,
		Now:               now,
	}, nil
}

func registrationKeyValid(key string) error {
	if key == "" {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("registration key is required"))
	}
	if len(key) > 256 {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("registration key is too long"))
	}
	return nil
}

func displayNameValid(name string) error {
	if !utf8.ValidString(name) || name != strings.TrimSpace(name) {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("computer name is invalid"))
	}
	size := utf8.RuneCountInString(name)
	if size < 1 || size > 100 {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("computer name must contain 1 to 100 characters"))
	}
	for _, character := range name {
		if unicode.IsControl(character) {
			return connect.NewError(connect.CodeInvalidArgument, errors.New("computer name is invalid"))
		}
	}
	return nil
}

func operatingSystem(value computerv1.OperatingSystem) (computerdomain.OperatingSystem, bool) {
	switch value {
	case computerv1.OperatingSystem_OPERATING_SYSTEM_MACOS:
		return computerdomain.OperatingSystemMacOS, true
	case computerv1.OperatingSystem_OPERATING_SYSTEM_LINUX:
		return computerdomain.OperatingSystemLinux, true
	default:
		return "", false
	}
}

func architecture(value computerv1.Architecture) (computerdomain.Architecture, bool) {
	switch value {
	case computerv1.Architecture_ARCHITECTURE_ARM64:
		return computerdomain.ArchitectureARM64, true
	case computerv1.Architecture_ARCHITECTURE_AMD64:
		return computerdomain.ArchitectureAMD64, true
	default:
		return "", false
	}
}

func sandboxCapability(value *computerv1.SandboxCapability) (computerdomain.SandboxCapability, error) {
	if value == nil || (value.GetProvider() == computerv1.SandboxProvider_SANDBOX_PROVIDER_UNSPECIFIED &&
		value.GetIsolation() == computerv1.SandboxIsolation_SANDBOX_ISOLATION_UNSPECIFIED &&
		value.GetWorkspaceAccess() == computerv1.SandboxWorkspaceAccess_SANDBOX_WORKSPACE_ACCESS_UNSPECIFIED &&
		value.GetProcessControl() == computerv1.SandboxProcessControl_SANDBOX_PROCESS_CONTROL_UNSPECIFIED &&
		value.GetFilesystemIsolation() == computerv1.SandboxFilesystemIsolation_SANDBOX_FILESYSTEM_ISOLATION_UNSPECIFIED &&
		value.GetNetworkIsolation() == computerv1.SandboxNetworkIsolation_SANDBOX_NETWORK_ISOLATION_UNSPECIFIED &&
		value.GetSecretMaterialization() == computerv1.SandboxSecretMaterialization_SANDBOX_SECRET_MATERIALIZATION_UNSPECIFIED &&
		value.GetDaemonCrashCleanup() == computerv1.SandboxDaemonCrashCleanup_SANDBOX_DAEMON_CRASH_CLEANUP_UNSPECIFIED) {
		return computerdomain.UnknownSandboxCapability(), nil
	}
	if value.GetProvider() == computerv1.SandboxProvider_SANDBOX_PROVIDER_TRUSTED_LOCAL &&
		value.GetIsolation() == computerv1.SandboxIsolation_SANDBOX_ISOLATION_TRUSTED_LOCAL &&
		value.GetWorkspaceAccess() == computerv1.SandboxWorkspaceAccess_SANDBOX_WORKSPACE_ACCESS_DIRECT_READ_WRITE &&
		value.GetProcessControl() == computerv1.SandboxProcessControl_SANDBOX_PROCESS_CONTROL_CONTEXT_PROCESS_GROUP &&
		value.GetFilesystemIsolation() == computerv1.SandboxFilesystemIsolation_SANDBOX_FILESYSTEM_ISOLATION_NONE &&
		value.GetNetworkIsolation() == computerv1.SandboxNetworkIsolation_SANDBOX_NETWORK_ISOLATION_NONE &&
		value.GetSecretMaterialization() == computerv1.SandboxSecretMaterialization_SANDBOX_SECRET_MATERIALIZATION_EPHEMERAL_ENVIRONMENT &&
		value.GetDaemonCrashCleanup() == computerv1.SandboxDaemonCrashCleanup_SANDBOX_DAEMON_CRASH_CLEANUP_NONE {
		return computerdomain.TrustedLocalSandboxCapability(), nil
	}
	return computerdomain.SandboxCapability{}, connect.NewError(connect.CodeInvalidArgument, errors.New("sandbox capability must be entirely unknown or the complete trusted-local declaration"))
}
