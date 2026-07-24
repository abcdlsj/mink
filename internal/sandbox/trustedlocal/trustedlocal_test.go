package trustedlocal

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	computerv1 "github.com/abcdlsj/sumi/gen/go/sumi/computer/v1"
	"github.com/abcdlsj/sumi/internal/sandbox"
	"github.com/google/uuid"
)

func TestTrustedLocalStartUsesExplicitEnvironmentAndCleansScratch(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	t.Setenv("AMBIENT_ONLY", "must-not-inherit")
	provider := mustProvider(t, Config{
		ScratchRoot: root,
		SecretLookup: func(key string) (string, bool) {
			if key == "TEST_SECRET" {
				return "secret-value", true
			}
			return "", false
		},
	})
	capability := provider.Capability()
	if capability.Provider != computerv1.SandboxProvider_SANDBOX_PROVIDER_TRUSTED_LOCAL ||
		capability.Isolation != computerv1.SandboxIsolation_SANDBOX_ISOLATION_TRUSTED_LOCAL ||
		capability.WorkspaceAccess != computerv1.SandboxWorkspaceAccess_SANDBOX_WORKSPACE_ACCESS_DIRECT_READ_WRITE ||
		capability.ProcessControl != computerv1.SandboxProcessControl_SANDBOX_PROCESS_CONTROL_CONTEXT_PROCESS_GROUP ||
		capability.FilesystemIsolation != computerv1.SandboxFilesystemIsolation_SANDBOX_FILESYSTEM_ISOLATION_NONE ||
		capability.NetworkIsolation != computerv1.SandboxNetworkIsolation_SANDBOX_NETWORK_ISOLATION_NONE ||
		capability.SecretMaterialization != computerv1.SandboxSecretMaterialization_SANDBOX_SECRET_MATERIALIZATION_EPHEMERAL_ENVIRONMENT ||
		capability.DaemonCrashCleanup != computerv1.SandboxDaemonCrashCleanup_SANDBOX_DAEMON_CRASH_CLEANUP_NONE {
		t.Fatalf("trusted-local capability = %+v", capability)
	}
	capability.Provider = computerv1.SandboxProvider_SANDBOX_PROVIDER_UNSPECIFIED
	if provider.Capability().Provider != computerv1.SandboxProvider_SANDBOX_PROVIDER_TRUSTED_LOCAL {
		t.Fatal("provider capability was mutable through returned value")
	}
	request := validRequest(workspace)
	request.Command = []string{"/bin/sh", "-c", "printf '%s|%s|%s|%s' \"$VISIBLE\" \"$HIDDEN\" \"$HOME\" \"$AMBIENT_ONLY\""}
	request.Environment = []sandbox.EnvironmentVariable{{Name: "VISIBLE", Value: "visible-value"}}
	request.Secrets = []sandbox.SecretEnvironmentVariable{{Name: "HIDDEN", Ref: sandbox.SecretRef{Source: SecretSourceComputerEnvironment, Key: "TEST_SECRET"}}}
	request.Stdout = &output
	process, err := provider.Start(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := uuid.Parse(process.RuntimeID()); err != nil {
		t.Fatalf("runtime id = %q: %v", process.RuntimeID(), err)
	}
	if err := process.Wait(); err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(output.String(), "|")
	if len(parts) != 4 || parts[0] != "visible-value" || parts[1] != "secret-value" || parts[3] != "" || !strings.HasPrefix(parts[2], root) {
		t.Fatalf("sandbox environment output = %q", output.String())
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("scratch after wait = %v", entries)
	}
}

func TestTrustedLocalDeclarationRejectsUnspecifiedAndUnknownEnums(t *testing.T) {
	declaration, err := Declaration()
	if err != nil {
		t.Fatal(err)
	}
	unspecified := declaration
	unspecified.NetworkIsolation = computerv1.SandboxNetworkIsolation_SANDBOX_NETWORK_ISOLATION_UNSPECIFIED
	if err := validateDeclaration(unspecified); err == nil {
		t.Fatal("unspecified declaration was accepted")
	}
	unknown := declaration
	unknown.Provider = computerv1.SandboxProvider(1000)
	if err := validateDeclaration(unknown); err == nil {
		t.Fatal("unknown declaration was accepted")
	}
}

func TestTrustedLocalStartFailureDoesNotExposeSecret(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	secret := "raw-start-failure-secret"
	provider := mustProvider(t, Config{ScratchRoot: root, SecretLookup: func(string) (string, bool) { return secret, true }})
	request := validRequest(workspace)
	request.Command = []string{"/does/not/exist"}
	request.Secrets = []sandbox.SecretEnvironmentVariable{{Name: "TOKEN", Ref: sandbox.SecretRef{Source: SecretSourceComputerEnvironment, Key: "TOKEN"}}}
	_, err := provider.Start(context.Background(), request)
	if err == nil || strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "TOKEN") {
		t.Fatalf("start error = %v", err)
	}
	entries, readErr := os.ReadDir(root)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("scratch after start failure = %v", entries)
	}
}

func TestTrustedLocalRejectsBeforeScratchOrProcess(t *testing.T) {
	for _, test := range []struct {
		name        string
		secretName  string
		secretFound bool
		wantLookups int
	}{
		{"reserved input", "HOME", true, 0},
		{"missing secret", "TOKEN", false, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			workspace := filepath.Join(t.TempDir(), "workspace")
			if err := os.Mkdir(workspace, 0o700); err != nil {
				t.Fatal(err)
			}
			lookups := 0
			provider := mustProvider(t, Config{
				ScratchRoot: root,
				SecretLookup: func(string) (string, bool) {
					lookups++
					return "raw-rejection-secret", test.secretFound
				},
			})
			marker := filepath.Join(workspace, "process-started")
			var stdout, stderr bytes.Buffer
			request := validRequest(workspace)
			request.Command = []string{"/bin/sh", "-c", "touch \"$MARKER\""}
			request.Environment = []sandbox.EnvironmentVariable{{Name: "MARKER", Value: marker}}
			request.Secrets = []sandbox.SecretEnvironmentVariable{{
				Name: test.secretName,
				Ref:  sandbox.SecretRef{Source: SecretSourceComputerEnvironment, Key: "SECRET_KEY"},
			}}
			request.Stdout = &stdout
			request.Stderr = &stderr
			_, err := provider.Start(context.Background(), request)
			if err == nil || strings.Contains(err.Error(), "SECRET_KEY") || strings.Contains(err.Error(), "raw-rejection-secret") {
				t.Fatalf("rejection error = %v", err)
			}
			if lookups != test.wantLookups {
				t.Fatalf("secret lookups = %d, want %d", lookups, test.wantLookups)
			}
			if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("process marker stat = %v", err)
			}
			entries, err := os.ReadDir(root)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
				t.Fatalf("rejection side effects = scratch %v, stdout %q, stderr %q", entries, stdout.String(), stderr.String())
			}
		})
	}
}

func TestTrustedLocalCancellationReapsProcessGroup(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	provider := mustProvider(t, Config{ScratchRoot: root, GracePeriod: 20 * time.Millisecond})
	request := validRequest(workspace)
	pidPath := filepath.Join(workspace, "child.pid")
	request.Environment = []sandbox.EnvironmentVariable{{Name: "PID_FILE", Value: pidPath}}
	request.Command = []string{"/bin/sh", "-c", "trap '' TERM; /bin/sh -c 'trap \"\" TERM; while :; do sleep 1; done' & echo $! > \"$PID_FILE\"; wait"}
	ctx, cancel := context.WithCancel(context.Background())
	process, err := provider.Start(ctx, request)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	var pidPayload []byte
	for len(pidPayload) == 0 && time.Now().Before(deadline) {
		pidPayload, _ = os.ReadFile(pidPath)
		time.Sleep(5 * time.Millisecond)
	}
	childPID, err := strconv.Atoi(strings.TrimSpace(string(pidPayload)))
	if err != nil {
		cancel()
		t.Fatalf("child pid = %q: %v", pidPayload, err)
	}
	cancel()
	waitContext, waitCancel := context.WithTimeout(context.Background(), time.Second)
	defer waitCancel()
	done := make(chan error, 1)
	go func() { done <- process.Wait() }()
	select {
	case <-waitContext.Done():
		t.Fatal("cancelled sandbox did not stop")
	case <-done:
	}
	if err := syscall.Kill(childPID, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("child process %d survived cancellation: %v", childPID, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("scratch after cancellation = %v", entries)
	}
}

func TestTrustedLocalReapsWithoutCallerWait(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	provider := mustProvider(t, Config{ScratchRoot: root})
	request := validRequest(workspace)
	request.Command = []string{"/bin/sh", "-c", "exit 0"}
	if _, err := provider.Start(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		entries, err := os.ReadDir(root)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("scratch was not reaped without Wait")
}

func TestTrustedLocalRejectsUnsafeRequestsWithoutScratch(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	base := validRequest(workspace)
	cases := []struct {
		name  string
		apply func(*sandbox.Request)
	}{
		{"relative command", func(request *sandbox.Request) { request.Command[0] = "sh" }},
		{"workspace permissions", func(request *sandbox.Request) {
			_ = os.Chmod(workspace, 0o755)
		}},
		{"duplicate environment", func(request *sandbox.Request) {
			request.Environment = []sandbox.EnvironmentVariable{{Name: "A", Value: "one"}, {Name: "A", Value: "two"}}
		}},
		{"managed environment", func(request *sandbox.Request) {
			request.Environment = []sandbox.EnvironmentVariable{{Name: "HOME", Value: "/tmp"}}
		}},
		{"managed PATH", func(request *sandbox.Request) {
			request.Environment = []sandbox.EnvironmentVariable{{Name: "PATH", Value: "/tmp"}}
		}},
		{"managed secret environment", func(request *sandbox.Request) {
			request.Secrets = []sandbox.SecretEnvironmentVariable{{Name: "HOME", Ref: sandbox.SecretRef{Source: SecretSourceComputerEnvironment, Key: "TOKEN"}}}
		}},
		{"secret collision", func(request *sandbox.Request) {
			request.Environment = []sandbox.EnvironmentVariable{{Name: "TOKEN", Value: "ordinary"}}
			request.Secrets = []sandbox.SecretEnvironmentVariable{{Name: "TOKEN", Ref: sandbox.SecretRef{Source: SecretSourceComputerEnvironment, Key: "TOKEN"}}}
		}},
		{"unsupported secret source", func(request *sandbox.Request) {
			request.Secrets = []sandbox.SecretEnvironmentVariable{{Name: "TOKEN", Ref: sandbox.SecretRef{Source: "other", Key: "TOKEN"}}}
		}},
		{"missing secret", func(request *sandbox.Request) {
			request.Secrets = []sandbox.SecretEnvironmentVariable{{Name: "TOKEN", Ref: sandbox.SecretRef{Source: SecretSourceComputerEnvironment, Key: "MISSING"}}}
		}},
		{"secret containing nul", func(request *sandbox.Request) {
			request.Secrets = []sandbox.SecretEnvironmentVariable{{Name: "TOKEN", Ref: sandbox.SecretRef{Source: SecretSourceComputerEnvironment, Key: "TOKEN"}}}
		}},
		{"secret too large", func(request *sandbox.Request) {
			request.Secrets = []sandbox.SecretEnvironmentVariable{{Name: "TOKEN", Ref: sandbox.SecretRef{Source: SecretSourceComputerEnvironment, Key: "TOKEN"}}}
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if test.name == "workspace permissions" {
				defer os.Chmod(workspace, 0o700)
			}
			request := base
			request.Environment = append([]sandbox.EnvironmentVariable(nil), base.Environment...)
			request.Secrets = append([]sandbox.SecretEnvironmentVariable(nil), base.Secrets...)
			if test.name == "secret too large" || test.name == "secret containing nul" {
				value := strings.Repeat("x", maxSecretBytes+1)
				if test.name == "secret containing nul" {
					value = "secret\x00suffix"
				}
				provider := mustProvider(t, Config{ScratchRoot: root, SecretLookup: func(string) (string, bool) { return value, true }})
				test.apply(&request)
				if _, err := provider.Start(context.Background(), request); err == nil {
					t.Fatalf("%s was accepted", test.name)
				}
				return
			}
			test.apply(&request)
			provider := mustProvider(t, Config{ScratchRoot: root})
			if _, err := provider.Start(context.Background(), request); err == nil {
				t.Fatal("unsafe sandbox request was accepted")
			}
			entries, err := os.ReadDir(root)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("scratch after rejected request = %v", entries)
			}
		})
	}
}

func validRequest(workspace string) sandbox.Request {
	return sandbox.Request{
		AgentID: uuid.NewString(), ComputerID: uuid.NewString(),
		RunID: uuid.NewString(), Attempt: 1, Fence: 1, PlacementDesiredRevision: 1,
		Workspace: workspace, Command: []string{"/bin/true"},
	}
}

func mustProvider(t *testing.T, config Config) *Provider {
	t.Helper()
	provider, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	return provider
}
