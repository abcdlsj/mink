package sandbox

import (
	"context"
	"io"

	computerv1 "github.com/abcdlsj/sumi/gen/go/sumi/computer/v1"
)

type Capability struct {
	Provider              computerv1.SandboxProvider
	Isolation             computerv1.SandboxIsolation
	WorkspaceAccess       computerv1.SandboxWorkspaceAccess
	ProcessControl        computerv1.SandboxProcessControl
	FilesystemIsolation   computerv1.SandboxFilesystemIsolation
	NetworkIsolation      computerv1.SandboxNetworkIsolation
	SecretMaterialization computerv1.SandboxSecretMaterialization
	DaemonCrashCleanup    computerv1.SandboxDaemonCrashCleanup
}

type EnvironmentVariable struct {
	Name  string
	Value string
}

type SecretRef struct {
	Source string
	Key    string
}

type SecretEnvironmentVariable struct {
	Name string
	Ref  SecretRef
}

type Request struct {
	AgentID             string
	ComputerID          string
	DeliveryID          string
	RunID               string
	LaunchID            string
	Fence               uint64
	PlacementGeneration uint64
	Workspace           string
	Command             []string
	Environment         []EnvironmentVariable
	Secrets             []SecretEnvironmentVariable
	Stdin               io.Reader
	Stdout              io.Writer
	Stderr              io.Writer
}

type Process interface {
	RuntimeID() string
	Wait() error
}

type Provider interface {
	Capability() Capability
	Start(context.Context, Request) (Process, error)
}
