package external

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"reflect"
	"testing"
	"time"

	"github.com/abcdlsj/sumi/internal/sandbox"
)

func TestEmitJSONLLinesPreservesRecordsAndBoundsOutput(t *testing.T) {
	input := []byte("{\"one\":1}\n{\"two\":2}\n")
	var lines []string
	if err := emitJSONLLines(bytesReader(input), int64(len(input)), func(line []byte) error {
		lines = append(lines, string(line))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if want := []string{`{"one":1}`, `{"two":2}`}; !reflect.DeepEqual(lines, want) {
		t.Fatalf("lines = %q, want %q", lines, want)
	}
	if err := emitJSONLLines(bytesReader(input), int64(len(input)-1), func([]byte) error { return nil }); err == nil {
		t.Fatal("stdout limit accepted oversized JSONL")
	}
	wantErr := errors.New("consumer stopped")
	if err := emitJSONLLines(bytesReader(input), int64(len(input)), func([]byte) error { return wantErr }); !errors.Is(err, wantErr) {
		t.Fatalf("consumer error = %v", err)
	}
	if err := drainBounded(bytesReader([]byte("stderr")), 5); err == nil {
		t.Fatal("stderr limit accepted oversized output")
	}
}

func TestProcessRunnerEmitsOnlyStdoutJSONL(t *testing.T) {
	path, err := exec.LookPath("sh")
	if err != nil {
		t.Fatal(err)
	}
	sandboxProvider := &scriptSandbox{stdout: "{\"type\":\"one\"}\n{\"type\":\"two\"}\n", stderr: "private diagnostic"}
	runner := ProcessRunner{
		Path: path, Sandbox: sandboxProvider, Timeout: time.Second,
		TerminationGrace: time.Millisecond, MaxOutputBytes: 1024,
	}
	var emitted []string
	if err := runner.Run(context.Background(), testBinding(), []byte("prompt"), func(line []byte) error {
		emitted = append(emitted, string(line))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if want := []string{`{"type":"one"}`, `{"type":"two"}`}; !reflect.DeepEqual(emitted, want) {
		t.Fatalf("emitted = %q, want %q", emitted, want)
	}
	if sandboxProvider.stdin != "prompt" {
		t.Fatalf("stdin = %q", sandboxProvider.stdin)
	}
}

func TestProcessRunnerCancellationStopsItsSandboxProcess(t *testing.T) {
	path, err := exec.LookPath("sh")
	if err != nil {
		t.Fatal(err)
	}
	sandboxProvider := &blockingSandbox{stopped: make(chan struct{})}
	runner := ProcessRunner{
		Path: path, Sandbox: sandboxProvider, Timeout: time.Second,
		TerminationGrace: time.Millisecond, MaxOutputBytes: 1024,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx, testBinding(), nil, func([]byte) error { return nil }) }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled runner did not return")
	}
	select {
	case <-sandboxProvider.stopped:
	case <-time.After(time.Second):
		t.Fatal("sandbox process did not observe cancellation")
	}
}

func testBinding() Binding {
	return Binding{
		AgentID: "agent", ComputerID: "computer", RunID: "run", Attempt: 1, Fence: 1,
		PlacementDesiredRevision: 1, Workspace: "/workspace",
	}
}

type stringReader struct {
	value []byte
	index int
}

func bytesReader(value []byte) *stringReader { return &stringReader{value: value} }

func (reader *stringReader) Read(destination []byte) (int, error) {
	if reader.index == len(reader.value) {
		return 0, io.EOF
	}
	count := copy(destination, reader.value[reader.index:])
	reader.index += count
	return count, nil
}

type scriptSandbox struct {
	stdout string
	stderr string
	stdin  string
}

func (*scriptSandbox) Capability() sandbox.Capability { return sandbox.Capability{} }

func (provider *scriptSandbox) Start(_ context.Context, request sandbox.Request) (sandbox.Process, error) {
	payload, err := io.ReadAll(request.Stdin)
	if err != nil {
		return nil, err
	}
	provider.stdin = string(payload)
	return &scriptProcess{request: request, stdout: provider.stdout, stderr: provider.stderr}, nil
}

type scriptProcess struct {
	request sandbox.Request
	stdout  string
	stderr  string
}

func (*scriptProcess) RuntimeID() string { return "script" }

func (process *scriptProcess) Wait() error {
	if _, err := io.WriteString(process.request.Stdout, process.stdout); err != nil {
		return err
	}
	if _, err := io.WriteString(process.request.Stderr, process.stderr); err != nil {
		return err
	}
	return nil
}

type blockingSandbox struct {
	stopped chan struct{}
}

func (*blockingSandbox) Capability() sandbox.Capability { return sandbox.Capability{} }

func (provider *blockingSandbox) Start(ctx context.Context, _ sandbox.Request) (sandbox.Process, error) {
	return &blockingProcess{ctx: ctx, stopped: provider.stopped}, nil
}

type blockingProcess struct {
	ctx     context.Context
	stopped chan struct{}
}

func (*blockingProcess) RuntimeID() string { return "blocking" }

func (process *blockingProcess) Wait() error {
	<-process.ctx.Done()
	close(process.stopped)
	return process.ctx.Err()
}
