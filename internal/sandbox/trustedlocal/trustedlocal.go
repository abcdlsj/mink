package trustedlocal

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	computerv1 "github.com/abcdlsj/sumi/gen/go/sumi/computer/v1"
	"github.com/abcdlsj/sumi/internal/sandbox"
	"github.com/google/uuid"
)

const (
	SecretSourceComputerEnvironment = "computer.environment"
	defaultGracePeriod              = 250 * time.Millisecond
	maxSecretBytes                  = 64 * 1024
)

type managedEnvironmentVariable struct {
	name      string
	value     func(string) string
	directory bool
}

var managedEnvironment = [...]managedEnvironmentVariable{
	{"HOME", func(root string) string { return filepath.Join(root, "home") }, true},
	{"TMPDIR", func(root string) string { return filepath.Join(root, "tmp") }, true},
	{"XDG_CONFIG_HOME", func(root string) string { return filepath.Join(root, "xdg-config") }, true},
	{"XDG_CACHE_HOME", func(root string) string { return filepath.Join(root, "xdg-cache") }, true},
	{"XDG_DATA_HOME", func(root string) string { return filepath.Join(root, "xdg-data") }, true},
	{"XDG_STATE_HOME", func(root string) string { return filepath.Join(root, "xdg-state") }, true},
	{"PATH", func(string) string { return "/usr/bin:/bin" }, false},
}

type Config struct {
	ScratchRoot  string
	SecretLookup func(string) (string, bool)
	GracePeriod  time.Duration
}

type Provider struct {
	config      Config
	declaration sandbox.Capability
}

func New(config Config) (*Provider, error) {
	if config.GracePeriod <= 0 {
		config.GracePeriod = defaultGracePeriod
	}
	declaration, err := Declaration()
	if err != nil {
		return nil, err
	}
	return &Provider{config: config, declaration: declaration}, nil
}

func Declaration() (sandbox.Capability, error) {
	declaration := sandbox.Capability{
		Provider:              computerv1.SandboxProvider_SANDBOX_PROVIDER_TRUSTED_LOCAL,
		Isolation:             computerv1.SandboxIsolation_SANDBOX_ISOLATION_TRUSTED_LOCAL,
		WorkspaceAccess:       computerv1.SandboxWorkspaceAccess_SANDBOX_WORKSPACE_ACCESS_DIRECT_READ_WRITE,
		ProcessControl:        computerv1.SandboxProcessControl_SANDBOX_PROCESS_CONTROL_CONTEXT_PROCESS_GROUP,
		FilesystemIsolation:   computerv1.SandboxFilesystemIsolation_SANDBOX_FILESYSTEM_ISOLATION_NONE,
		NetworkIsolation:      computerv1.SandboxNetworkIsolation_SANDBOX_NETWORK_ISOLATION_NONE,
		SecretMaterialization: computerv1.SandboxSecretMaterialization_SANDBOX_SECRET_MATERIALIZATION_EPHEMERAL_ENVIRONMENT,
		DaemonCrashCleanup:    computerv1.SandboxDaemonCrashCleanup_SANDBOX_DAEMON_CRASH_CLEANUP_NONE,
	}
	if err := validateDeclaration(declaration); err != nil {
		return sandbox.Capability{}, err
	}
	return declaration, nil
}

func (p *Provider) Capability() sandbox.Capability {
	return p.declaration
}

func validateDeclaration(declaration sandbox.Capability) error {
	values := []struct {
		value int32
		known map[int32]string
	}{
		{int32(declaration.Provider), computerv1.SandboxProvider_name},
		{int32(declaration.Isolation), computerv1.SandboxIsolation_name},
		{int32(declaration.WorkspaceAccess), computerv1.SandboxWorkspaceAccess_name},
		{int32(declaration.ProcessControl), computerv1.SandboxProcessControl_name},
		{int32(declaration.FilesystemIsolation), computerv1.SandboxFilesystemIsolation_name},
		{int32(declaration.NetworkIsolation), computerv1.SandboxNetworkIsolation_name},
		{int32(declaration.SecretMaterialization), computerv1.SandboxSecretMaterialization_name},
		{int32(declaration.DaemonCrashCleanup), computerv1.SandboxDaemonCrashCleanup_name},
	}
	for _, value := range values {
		if value.value == 0 {
			return errors.New("sandbox capability is unspecified")
		}
		if _, found := value.known[value.value]; !found {
			return errors.New("sandbox capability is unknown")
		}
	}
	return nil
}

func (p *Provider) Start(ctx context.Context, request sandbox.Request) (sandbox.Process, error) {
	if err := validateRequest(request); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	secretValues, err := p.resolveSecrets(request.Secrets)
	if err != nil {
		return nil, err
	}
	scratch, err := p.createScratch()
	if err != nil {
		clearStrings(secretValues)
		return nil, errors.New("create sandbox scratch failed")
	}
	if err := createScratchDirectories(scratch); err != nil {
		clearStrings(secretValues)
		removeScratch(scratch)
		return nil, errors.New("create sandbox directories failed")
	}
	environment := buildEnvironment(request, scratch, secretValues)
	runtimeID := uuid.NewString()
	command := exec.Command(request.Command[0], request.Command[1:]...)
	command.Dir = request.Workspace
	command.Env = environment
	command.Stdin = request.Stdin
	command.Stdout = request.Stdout
	command.Stderr = request.Stderr
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		clearStrings(environment)
		clearStrings(secretValues)
		removeScratch(scratch)
		return nil, errors.New("start sandbox process failed")
	}
	clearStrings(environment)
	clearStrings(secretValues)
	command.Env = nil

	process := &localProcess{runtimeID: runtimeID, result: make(chan error, 1)}
	runContext, cancel := context.WithCancel(ctx)
	go p.manageProcess(runContext, cancel, command, scratch, process)
	return process, nil
}

func (p *Provider) resolveSecrets(refs []sandbox.SecretEnvironmentVariable) ([]string, error) {
	values := make([]string, 0, len(refs))
	lookup := p.config.SecretLookup
	if lookup == nil {
		lookup = os.LookupEnv
	}
	for _, secret := range refs {
		if secret.Ref.Source != SecretSourceComputerEnvironment {
			clearStrings(values)
			return nil, errors.New("secret source is unsupported")
		}
		value, found := lookup(secret.Ref.Key)
		if !found || strings.IndexByte(value, 0) >= 0 || len(value) > maxSecretBytes {
			clearStrings(values)
			return nil, errors.New("secret reference is unavailable")
		}
		values = append(values, value)
	}
	return values, nil
}

func (p *Provider) createScratch() (string, error) {
	root := p.config.ScratchRoot
	if root == "" {
		root = os.TempDir()
	}
	if !filepath.IsAbs(root) {
		return "", errors.New("scratch root is not absolute")
	}
	if info, err := os.Lstat(root); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("scratch root is invalid")
	}
	scratch, err := os.MkdirTemp(root, "sumi-sandbox-")
	if err != nil {
		return "", err
	}
	if err := os.Chmod(scratch, 0o700); err != nil {
		removeScratch(scratch)
		return "", err
	}
	return scratch, nil
}

func createScratchDirectories(scratch string) error {
	for _, variable := range managedEnvironment {
		if !variable.directory {
			continue
		}
		path := variable.value(scratch)
		if err := os.Mkdir(path, 0o700); err != nil {
			return err
		}
	}
	return nil
}

func buildEnvironment(request sandbox.Request, scratch string, secretValues []string) []string {
	environment := make([]string, 0, len(request.Environment)+len(request.Secrets)+len(managedEnvironment))
	for _, value := range request.Environment {
		environment = append(environment, value.Name+"="+value.Value)
	}
	for _, variable := range managedEnvironment {
		environment = append(environment, variable.name+"="+variable.value(scratch))
	}
	for index, secret := range request.Secrets {
		environment = append(environment, secret.Name+"="+secretValues[index])
	}
	return environment
}

func validateRequest(request sandbox.Request) error {
	for _, value := range []string{request.AgentID, request.ComputerID, request.DeliveryID, request.RunID, request.LaunchID} {
		parsed, err := uuid.Parse(value)
		if err != nil || parsed.String() != value {
			return errors.New("sandbox binding is invalid")
		}
	}
	if request.Fence == 0 || request.PlacementGeneration == 0 || len(request.Command) == 0 || !filepath.IsAbs(request.Command[0]) {
		return errors.New("sandbox request is invalid")
	}
	for _, argument := range request.Command {
		if strings.IndexByte(argument, 0) >= 0 {
			return errors.New("sandbox command is invalid")
		}
	}
	if err := validateWorkspace(request.Workspace); err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(request.Environment)+len(request.Secrets)+len(managedEnvironment))
	reserved := make(map[string]struct{}, len(managedEnvironment))
	for _, variable := range managedEnvironment {
		reserved[variable.name] = struct{}{}
	}
	for _, value := range request.Environment {
		if !validEnvironmentName(value.Name) || strings.IndexByte(value.Value, 0) >= 0 {
			return errors.New("sandbox environment is invalid")
		}
		if _, found := reserved[value.Name]; found {
			return errors.New("sandbox environment overrides managed value")
		}
		if _, found := seen[value.Name]; found {
			return errors.New("sandbox environment is duplicated")
		}
		seen[value.Name] = struct{}{}
	}
	for _, value := range request.Secrets {
		if !validEnvironmentName(value.Name) || value.Ref.Key == "" || strings.IndexByte(value.Ref.Key, 0) >= 0 || strings.ContainsRune(value.Ref.Key, '=') {
			return errors.New("sandbox secret reference is invalid")
		}
		if _, found := reserved[value.Name]; found {
			return errors.New("sandbox secret environment overrides managed value")
		}
		if _, found := seen[value.Name]; found {
			return errors.New("sandbox secret environment is duplicated")
		}
		seen[value.Name] = struct{}{}
	}
	return nil
}

func validateWorkspace(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("sandbox workspace is not absolute")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o700 {
		return errors.New("sandbox workspace is invalid")
	}
	return nil
}

func validEnvironmentName(value string) bool {
	return value != "" && strings.IndexByte(value, 0) < 0 && !strings.ContainsRune(value, '=')
}

func (p *Provider) manageProcess(ctx context.Context, cancel context.CancelFunc, command *exec.Cmd, scratch string, process *localProcess) {
	defer cancel()
	waitResult := make(chan error, 1)
	go func() { waitResult <- command.Wait() }()
	select {
	case err := <-waitResult:
		reapProcessGroup(command.Process.Pid, p.config.GracePeriod)
		process.finish(err, scratch)
	case <-ctx.Done():
		process.finish(terminateProcessGroup(command.Process.Pid, p.config.GracePeriod, waitResult), scratch)
	}
}

func terminateProcessGroup(pid int, grace time.Duration, waitResult <-chan error) error {
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	timer := time.NewTimer(grace)
	defer timer.Stop()
	var result error
	waited := false
	select {
	case result = <-waitResult:
		waited = true
		<-timer.C
	case <-timer.C:
	}
	if syscall.Kill(-pid, 0) == nil {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}
	if !waited {
		result = <-waitResult
	}
	return result
}

func reapProcessGroup(pid int, grace time.Duration) {
	if syscall.Kill(-pid, 0) != nil {
		return
	}
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	timer := time.NewTimer(grace)
	defer timer.Stop()
	<-timer.C
	if syscall.Kill(-pid, 0) == nil {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}
}

func clearStrings(values []string) {
	for index := range values {
		values[index] = ""
	}
}

func removeScratch(path string) {
	if err := os.RemoveAll(path); err != nil {
		return
	}
}

type localProcess struct {
	runtimeID string
	result    chan error
	once      sync.Once
	mu        sync.Mutex
	err       error
}

func (p *localProcess) RuntimeID() string {
	return p.runtimeID
}

func (p *localProcess) Wait() error {
	p.once.Do(func() { p.err = <-p.result })
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}

func (p *localProcess) finish(err error, scratch string) {
	removeScratch(scratch)
	p.mu.Lock()
	p.err = err
	p.mu.Unlock()
	p.result <- err
}

var _ sandbox.Provider = (*Provider)(nil)
