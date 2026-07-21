package driver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestProcessRunnerUsesExplicitEnvironmentAndJSONL(t *testing.T) {
	runner := testProcessRunner("result")
	var lines [][]byte
	if err := runner.Run(context.Background(), []byte("input"), func(line []byte) error {
		lines = append(lines, append([]byte(nil), line...))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got, want := string(lines[0]), `{"type":"result","result":{"outcome":"succeeded","body":"done"}}`; len(lines) != 1 || got != want {
		t.Fatalf("lines = %q, want %q", lines, want)
	}
}

func TestProcessRunnerBoundsOutputAndTerminatesOnTimeout(t *testing.T) {
	t.Run("stdout", func(t *testing.T) {
		runner := testProcessRunner("stdout-overflow")
		runner.MaxOutputBytes = 32
		err := runner.Run(context.Background(), nil, func([]byte) error { return nil })
		if err == nil || !strings.Contains(err.Error(), "stdout exceeds limit") {
			t.Fatalf("overflow error = %v", err)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		runner := testProcessRunner("ignore-term")
		runner.Timeout = 20 * time.Millisecond
		runner.TerminationGrace = 20 * time.Millisecond
		started := time.Now()
		err := runner.Run(context.Background(), nil, func([]byte) error { return nil })
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("timeout error = %v", err)
		}
		if elapsed := time.Since(started); elapsed > time.Second {
			t.Fatalf("timeout cleanup took %v", elapsed)
		}
	})
}

func TestProcessRunnerRejectsUnboundedOrAmbientConfiguration(t *testing.T) {
	tests := []ProcessRunner{
		{Path: "relative", Timeout: time.Second, TerminationGrace: time.Second, MaxOutputBytes: 1},
		{Path: os.Args[0], Timeout: 0, TerminationGrace: time.Second, MaxOutputBytes: 1},
		{Path: os.Args[0], Timeout: time.Second, TerminationGrace: time.Second, MaxOutputBytes: 1, Env: []string{"A=1", "A=2"}},
	}
	for _, runner := range tests {
		if err := runner.Run(context.Background(), nil, func([]byte) error { return nil }); err == nil {
			t.Fatal("invalid process runner started")
		}
	}
}

func testProcessRunner(mode string) ProcessRunner {
	return ProcessRunner{
		Path: os.Args[0], Args: []string{"-test.run=^TestProcessRunnerHelper$", "--", mode},
		Env:     []string{"SUMI_TEST_PROCESS_RUNNER=1", "SUMI_RUNNER_PROOF=explicit"},
		Timeout: 5 * time.Second, TerminationGrace: 50 * time.Millisecond, MaxOutputBytes: 1024,
	}
}

func TestProcessRunnerHelper(t *testing.T) {
	if os.Getenv("SUMI_TEST_PROCESS_RUNNER") != "1" {
		return
	}
	mode := os.Args[len(os.Args)-1]
	if os.Getenv("SUMI_RUNNER_PROOF") != "explicit" || os.Getenv("HOME") != "" {
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
