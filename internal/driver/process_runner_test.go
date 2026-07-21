package driver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/abcdlsj/sumi/internal/sandbox"
	"github.com/abcdlsj/sumi/internal/sandbox/trustedlocal"
	"github.com/google/uuid"
)

func TestProcessRunnerUsesExplicitEnvironmentAndJSONL(t *testing.T) {
	runner, command := testProcessRunner(t, "result")
	var lines [][]byte
	if err := runner.Run(context.Background(), command, []byte("input"), func(line []byte) error {
		lines = append(lines, append([]byte(nil), line...))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got, want := string(lines[0]), `{"type":"result","result":{"outcome":"succeeded","body":"done"}}`; len(lines) != 1 || got != want {
		t.Fatalf("lines = %q, want %q", lines, want)
	}
}

func TestProcessRunnerBindsRunInputToSandboxRequest(t *testing.T) {
	runner, command := testProcessRunner(t, "result")
	provider := &recordingProvider{}
	runner.Provider = provider
	if err := runner.Run(context.Background(), command, []byte("input"), func([]byte) error { return nil }); err != nil {
		t.Fatal(err)
	}
	input := command.Input
	request := provider.request
	if request.AgentID != input.AgentID || request.ComputerID != input.ComputerID || request.DeliveryID != input.DeliveryID ||
		request.RunID != input.RunID || request.LaunchID != input.LaunchID || request.Fence != input.Fence ||
		request.PlacementGeneration != input.Generation || request.Workspace != input.Workspace {
		t.Fatalf("sandbox binding = %+v", request)
	}
	if want := append([]string{runner.Path}, runner.Args...); !reflect.DeepEqual(request.Command, want) {
		t.Fatalf("sandbox command = %q, want %q", request.Command, want)
	}
	if len(request.Environment) != 0 || !reflect.DeepEqual(request.Secrets, runner.Secrets) {
		t.Fatalf("sandbox environment/secrets = %+v/%+v", request.Environment, request.Secrets)
	}
}

func TestProcessRunnerBoundsOutputAndTerminatesOnTimeout(t *testing.T) {
	t.Run("stdout", func(t *testing.T) {
		runner, command := testProcessRunner(t, "stdout-overflow")
		runner.MaxOutputBytes = 32
		err := runner.Run(context.Background(), command, nil, func([]byte) error { return nil })
		if err == nil || !strings.Contains(err.Error(), "stdout exceeds limit") {
			t.Fatalf("overflow error = %v", err)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		runner, command := testProcessRunner(t, "ignore-term")
		runner.Timeout = 20 * time.Millisecond
		runner.TerminationGrace = 20 * time.Millisecond
		started := time.Now()
		err := runner.Run(context.Background(), command, nil, func([]byte) error { return nil })
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("timeout error = %v", err)
		}
		if elapsed := time.Since(started); elapsed > time.Second {
			t.Fatalf("timeout cleanup took %v", elapsed)
		}
	})
}

func TestProcessRunnerRejectsUnboundedOrAmbientConfiguration(t *testing.T) {
	valid, command := testProcessRunner(t, "result")
	tests := []ProcessRunner{
		{Path: "relative", Provider: valid.Provider, Timeout: time.Second, TerminationGrace: time.Second, MaxOutputBytes: 1},
		{Path: os.Args[0], Provider: valid.Provider, Timeout: 0, TerminationGrace: time.Second, MaxOutputBytes: 1},
		{Path: os.Args[0], Timeout: time.Second, TerminationGrace: time.Second, MaxOutputBytes: 1},
	}
	for _, runner := range tests {
		if err := runner.Run(context.Background(), command, nil, func([]byte) error { return nil }); err == nil {
			t.Fatal("invalid process runner started")
		}
	}
}

func testProcessRunner(t *testing.T, mode string) (ProcessRunner, Command) {
	t.Helper()
	provider, err := trustedlocal.New(trustedlocal.Config{
		ScratchRoot: t.TempDir(), GracePeriod: 50 * time.Millisecond,
		SecretLookup: func(key string) (string, bool) {
			values := map[string]string{"PROCESS_RUNNER_ENABLED": "1", "PROCESS_RUNNER_PROOF": "explicit"}
			value, found := values[key]
			return value, found
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	if err := os.Chmod(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	input := testInput()
	input.AgentID = uuid.NewString()
	input.ComputerID = uuid.NewString()
	input.DeliveryID = uuid.NewString()
	input.RunID = uuid.NewString()
	input.LaunchID = uuid.NewString()
	input.Workspace = workspace
	runner := ProcessRunner{
		Path: os.Args[0], Args: []string{"-test.run=^TestProcessRunnerHelper$", "--", mode},
		Provider: provider,
		Secrets: []sandbox.SecretEnvironmentVariable{
			{Name: "SUMI_TEST_PROCESS_RUNNER", Ref: sandbox.SecretRef{Source: trustedlocal.SecretSourceComputerEnvironment, Key: "PROCESS_RUNNER_ENABLED"}},
			{Name: "SUMI_RUNNER_PROOF", Ref: sandbox.SecretRef{Source: trustedlocal.SecretSourceComputerEnvironment, Key: "PROCESS_RUNNER_PROOF"}},
		},
		Timeout: 5 * time.Second, TerminationGrace: 50 * time.Millisecond, MaxOutputBytes: 1024,
	}
	return runner, Command{Kind: CommandPrompt, Input: &input}
}

func TestProcessRunnerHelper(t *testing.T) {
	if os.Getenv("SUMI_TEST_PROCESS_RUNNER") != "1" {
		return
	}
	mode := os.Args[len(os.Args)-1]
	if os.Getenv("SUMI_RUNNER_PROOF") != "explicit" || os.Getenv("HOME") == "" {
		os.Exit(2)
	}
	switch mode {
	case "result":
		input, err := io.ReadAll(os.Stdin)
		if err != nil || string(input) != "input" {
			os.Exit(3)
		}
		fmt.Fprintln(os.Stdout, `{"type":"result","result":{"outcome":"succeeded","body":"done"}}`)
	case "stdout-overflow":
		_, _ = io.WriteString(os.Stdout, strings.Repeat("x", 1025))
	case "ignore-term":
		signal.Ignore(syscall.SIGTERM)
		for {
			time.Sleep(time.Hour)
		}
	default:
		os.Exit(4)
	}
	os.Exit(0)
}

type recordingProvider struct {
	request sandbox.Request
}

func (p *recordingProvider) Capability() sandbox.Capability {
	return sandbox.Capability{}
}

func (p *recordingProvider) Start(_ context.Context, request sandbox.Request) (sandbox.Process, error) {
	p.request = request
	process := recordingProcess{done: make(chan error, 1)}
	go func() {
		_, err := io.WriteString(request.Stdout, `{"type":"result","result":{"outcome":"succeeded","body":"done"}}`+"\n")
		process.done <- err
	}()
	return process, nil
}

type recordingProcess struct {
	done chan error
}

func (p recordingProcess) RuntimeID() string {
	return "recording"
}

func (p recordingProcess) Wait() error {
	return <-p.done
}
