package host

import (
	computerv1 "github.com/abcdlsj/sumi/gen/go/sumi/computer/v1"
	"github.com/abcdlsj/sumi/internal/sandbox/trustedlocal"
)

func TrustedLocalSandboxCapability() (*computerv1.SandboxCapability, error) {
	declaration, err := trustedlocal.Declaration()
	if err != nil {
		return nil, err
	}
	return &computerv1.SandboxCapability{
		Provider:              declaration.Provider,
		Isolation:             declaration.Isolation,
		WorkspaceAccess:       declaration.WorkspaceAccess,
		ProcessControl:        declaration.ProcessControl,
		FilesystemIsolation:   declaration.FilesystemIsolation,
		NetworkIsolation:      declaration.NetworkIsolation,
		SecretMaterialization: declaration.SecretMaterialization,
		DaemonCrashCleanup:    declaration.DaemonCrashCleanup,
	}, nil
}
