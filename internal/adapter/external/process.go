package external

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/abcdlsj/sumi/internal/observability"
	"github.com/abcdlsj/sumi/internal/sandbox"
)

type Binding struct {
	AgentID                  string
	ComputerID               string
	RunID                    string
	Attempt                  uint64
	Fence                    uint64
	PlacementDesiredRevision uint64
	Workspace                string
}

type ProcessRunner struct {
	Path             string
	Args             []string
	Secrets          []sandbox.SecretEnvironmentVariable
	Sandbox          sandbox.Provider
	Timeout          time.Duration
	TerminationGrace time.Duration
	MaxOutputBytes   int64
	Logger           *observability.Logger
}

func (r ProcessRunner) Validate() error {
	if !filepath.IsAbs(r.Path) {
		return errors.New("external adapter executable path must be absolute")
	}
	info, err := os.Stat(r.Path)
	if err != nil {
		return fmt.Errorf("stat external adapter executable: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return errors.New("external adapter executable is invalid")
	}
	if r.Timeout <= 0 || r.TerminationGrace <= 0 || r.MaxOutputBytes <= 0 || r.Sandbox == nil {
		return errors.New("external adapter sandbox and runtime bounds are required")
	}
	for _, arg := range r.Args {
		if strings.IndexByte(arg, 0) >= 0 {
			return errors.New("external adapter argument contains NUL")
		}
	}
	return nil
}

func (r ProcessRunner) Run(ctx context.Context, binding Binding, input []byte, emit func([]byte) error) error {
	if err := r.Validate(); err != nil {
		return err
	}
	if emit == nil || binding.AgentID == "" || binding.ComputerID == "" || binding.RunID == "" || binding.Attempt == 0 || binding.Fence == 0 || binding.PlacementDesiredRevision == 0 || binding.Workspace == "" {
		return errors.New("external adapter run binding is incomplete")
	}
	logger := observability.CategoryLogger(r.Logger, observability.ComponentComputer, observability.CategoryEngine)
	started := time.Now()
	logger.Info("external adapter process starting", "event", "adapter.process.starting", "agent_id", binding.AgentID, "run_id", binding.RunID, "attempt", binding.Attempt, "fence", binding.Fence, "executable", filepath.Base(r.Path), "arguments", len(r.Args), "secret_refs", len(r.Secrets), "timeout", r.Timeout, "output_limit", r.MaxOutputBytes)
	runCtx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()
	stdoutReader, stdoutWriter := io.Pipe()
	stderrReader, stderrWriter := io.Pipe()
	process, err := r.Sandbox.Start(runCtx, sandbox.Request{
		AgentID: binding.AgentID, ComputerID: binding.ComputerID, RunID: binding.RunID,
		Attempt: binding.Attempt, Fence: binding.Fence, PlacementDesiredRevision: binding.PlacementDesiredRevision,
		Workspace: binding.Workspace, Command: append([]string{r.Path}, r.Args...), Secrets: r.Secrets,
		Stdin: bytes.NewReader(input), Stdout: stdoutWriter, Stderr: stderrWriter,
	})
	if err != nil {
		stdoutWriter.Close()
		stderrWriter.Close()
		stdoutReader.Close()
		stderrReader.Close()
		return fmt.Errorf("start external adapter: %w", err)
	}
	var closeOnce sync.Once
	closeReaders := func() { closeOnce.Do(func() { stdoutReader.Close(); stderrReader.Close() }) }
	stdoutDone := make(chan error, 1)
	go func() { stdoutDone <- emitJSONLLines(stdoutReader, r.MaxOutputBytes, emit) }()
	stderrDone := make(chan error, 1)
	go func() { stderrDone <- drainBounded(stderrReader, r.MaxOutputBytes) }()
	waitDone := make(chan error, 1)
	go func() {
		waitErr := process.Wait()
		stdoutWriter.Close()
		stderrWriter.Close()
		waitDone <- waitErr
	}()
	var stdoutErr, stderrErr, waitErr, failure error
	stdoutFinished, stderrFinished, waitFinished := false, false, false
	contextDone := runCtx.Done()
	for !stdoutFinished || !stderrFinished || !waitFinished {
		select {
		case stdoutErr = <-stdoutDone:
			stdoutFinished = true
			if stdoutErr != nil && failure == nil {
				failure = stdoutErr
				closeReaders()
				cancel()
			}
		case stderrErr = <-stderrDone:
			stderrFinished = true
			if stderrErr != nil && failure == nil {
				failure = stderrErr
				closeReaders()
				cancel()
			}
		case waitErr = <-waitDone:
			waitFinished = true
		case <-contextDone:
			contextDone = nil
			if failure == nil {
				failure = runCtx.Err()
				closeReaders()
				cancel()
			}
		}
	}
	if failure != nil {
		return failure
	}
	if stdoutErr != nil {
		return stdoutErr
	}
	if stderrErr != nil {
		return stderrErr
	}
	if waitErr != nil {
		return errors.New("external adapter process failed")
	}
	logger.Info("external adapter process exited", "event", "adapter.process.exited", "agent_id", binding.AgentID, "run_id", binding.RunID, "attempt", binding.Attempt, "duration", time.Since(started))
	return nil
}

func emitJSONLLines(reader io.Reader, maximum int64, emit func([]byte) error) error {
	scanner := bufio.NewScanner(io.LimitReader(reader, maximum+1))
	scanner.Buffer(make([]byte, 64*1024), int(maximum)+1)
	var total int64
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		total += int64(len(line)) + 1
		if total > maximum {
			return errors.New("external adapter stdout exceeds limit")
		}
		if err := emit(line); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read external adapter stdout: %w", err)
	}
	return nil
}

func drainBounded(reader io.Reader, maximum int64) error {
	output, err := io.ReadAll(io.LimitReader(reader, maximum+1))
	if err != nil {
		return fmt.Errorf("read external adapter stderr: %w", err)
	}
	if int64(len(output)) > maximum {
		return errors.New("external adapter stderr exceeds limit")
	}
	return nil
}
