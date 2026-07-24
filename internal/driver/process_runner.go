package driver

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

type ProcessRunner struct {
	Path             string
	Args             []string
	Secrets          []sandbox.SecretEnvironmentVariable
	Provider         sandbox.Provider
	Timeout          time.Duration
	TerminationGrace time.Duration
	MaxOutputBytes   int64
	Logger           *observability.Logger
}

func (r ProcessRunner) Validate() error {
	return r.validate()
}

func (r ProcessRunner) Run(ctx context.Context, command Command, input []byte, emit func([]byte) error) error {
	if err := r.validate(); err != nil {
		return err
	}
	if emit == nil {
		return errors.New("external driver line handler is required")
	}
	if command.Kind != CommandPrompt || command.Input == nil {
		return errors.New("external driver command binding is required")
	}
	logger := observability.CategoryLogger(r.Logger, observability.ComponentComputer, observability.CategoryDriver)
	started := time.Now()
	logger.Info("external driver process starting", "event", "driver.process.starting", "agent_id", command.Input.AgentID, "run_id", command.Input.RunID, "launch_id", command.Input.LaunchID, "fence", command.Input.Fence, "executable", filepath.Base(r.Path), "arguments", len(r.Args), "secret_refs", len(r.Secrets), "timeout", r.Timeout, "output_limit", r.MaxOutputBytes)
	runCtx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()
	stdoutReader, stdoutWriter := io.Pipe()
	stderrReader, stderrWriter := io.Pipe()
	process, err := r.Provider.Start(runCtx, sandbox.Request{
		AgentID: command.Input.AgentID, ComputerID: command.Input.ComputerID, DeliveryID: command.Input.DeliveryID,
		RunID: command.Input.RunID, LaunchID: command.Input.LaunchID, Fence: command.Input.Fence,
		PlacementGeneration: command.Input.Generation, Workspace: command.Input.Workspace,
		Command: append([]string{r.Path}, r.Args...), Secrets: r.Secrets, Stdin: bytes.NewReader(input),
		Stdout: stdoutWriter, Stderr: stderrWriter,
	})
	if err != nil {
		_ = stdoutWriter.Close()
		_ = stderrWriter.Close()
		_ = stdoutReader.Close()
		_ = stderrReader.Close()
		logger.Error("external driver process failed to start", "event", "driver.process.start.failed", "agent_id", command.Input.AgentID, "run_id", command.Input.RunID, "launch_id", command.Input.LaunchID, "err", err)
		return fmt.Errorf("start external driver: %w", err)
	}
	logger.Info("external driver process started", "event", "driver.process.started", "agent_id", command.Input.AgentID, "run_id", command.Input.RunID, "launch_id", command.Input.LaunchID)
	var closeReadersOnce sync.Once
	closeReaders := func() {
		closeReadersOnce.Do(func() {
			_ = stdoutReader.Close()
			_ = stderrReader.Close()
		})
	}
	stdoutDone := make(chan error, 1)
	go func() { stdoutDone <- emitJSONLLines(stdoutReader, r.MaxOutputBytes, emit) }()
	stderrDone := make(chan error, 1)
	go func() { stderrDone <- drainBounded(stderrReader, r.MaxOutputBytes) }()
	waitDone := make(chan error, 1)
	go func() {
		waitErr := process.Wait()
		_ = stdoutWriter.Close()
		_ = stderrWriter.Close()
		waitDone <- waitErr
	}()

	var stdoutErr, stderrErr, waitErr, contextErr error
	var failure error
	var stopOnce sync.Once
	stop := func(err error) {
		stopOnce.Do(func() {
			failure = err
			closeReaders()
			cancel()
		})
	}
	stdoutFinished := false
	stderrFinished := false
	waitFinished := false
	contextFinished := false
	contextDone := runCtx.Done()
	for !stdoutFinished || !stderrFinished || !waitFinished {
		select {
		case stdoutErr = <-stdoutDone:
			stdoutFinished = true
			if stdoutErr != nil {
				if contextErr = runCtx.Err(); contextErr != nil {
					contextFinished = true
					contextDone = nil
					stop(contextErr)
				} else {
					stop(stdoutErr)
				}
			}
		case stderrErr = <-stderrDone:
			stderrFinished = true
			if stderrErr != nil {
				if contextErr = runCtx.Err(); contextErr != nil {
					contextFinished = true
					contextDone = nil
					stop(contextErr)
				} else {
					stop(stderrErr)
				}
			}
		case waitErr = <-waitDone:
			waitFinished = true
		case <-contextDone:
			if !contextFinished {
				contextErr = runCtx.Err()
				contextFinished = true
				contextDone = nil
				stop(contextErr)
			}
		}
	}
	if failure != nil {
		logger.Warn("external driver process interrupted", "event", "driver.process.interrupted", "agent_id", command.Input.AgentID, "run_id", command.Input.RunID, "launch_id", command.Input.LaunchID, "duration", time.Since(started), "err", failure)
		return failure
	}
	if stdoutErr != nil {
		logger.Warn("external driver stdout handling failed", "event", "driver.stdout.failed", "agent_id", command.Input.AgentID, "run_id", command.Input.RunID, "launch_id", command.Input.LaunchID, "duration", time.Since(started), "err", stdoutErr)
		return stdoutErr
	}
	if stderrErr != nil {
		logger.Warn("external driver stderr handling failed", "event", "driver.stderr.failed", "agent_id", command.Input.AgentID, "run_id", command.Input.RunID, "launch_id", command.Input.LaunchID, "duration", time.Since(started), "err", stderrErr)
		return stderrErr
	}
	if contextErr != nil {
		logger.Warn("external driver context ended", "event", "driver.context.ended", "agent_id", command.Input.AgentID, "run_id", command.Input.RunID, "launch_id", command.Input.LaunchID, "duration", time.Since(started), "err", contextErr)
		return contextErr
	}
	if waitErr != nil {
		logger.Warn("external driver process exited unsuccessfully", "event", "driver.process.exit.failed", "agent_id", command.Input.AgentID, "run_id", command.Input.RunID, "launch_id", command.Input.LaunchID, "duration", time.Since(started), "err", waitErr)
		return errors.New("external driver process failed")
	}
	logger.Info("external driver process exited", "event", "driver.process.exited", "agent_id", command.Input.AgentID, "run_id", command.Input.RunID, "launch_id", command.Input.LaunchID, "duration", time.Since(started))
	return nil
}

func (r ProcessRunner) validate() error {
	if !filepath.IsAbs(r.Path) {
		return errors.New("external driver executable path must be absolute")
	}
	info, err := os.Stat(r.Path)
	if err != nil {
		return fmt.Errorf("stat external driver executable: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return errors.New("external driver executable is invalid")
	}
	if r.Timeout <= 0 || r.TerminationGrace <= 0 || r.MaxOutputBytes <= 0 {
		return errors.New("external driver runtime bounds are required")
	}
	if r.Provider == nil {
		return errors.New("external driver sandbox provider is required")
	}
	for _, argument := range r.Args {
		if strings.IndexByte(argument, 0) >= 0 {
			return errors.New("external driver argument contains NUL")
		}
	}
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
			return errors.New("external driver stdout exceeds limit")
		}
		if err := emit(line); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read external driver stdout: %w", err)
	}
	return nil
}

func drainBounded(reader io.Reader, maximum int64) error {
	output, err := io.ReadAll(io.LimitReader(reader, maximum+1))
	if err != nil {
		return fmt.Errorf("read external driver stderr: %w", err)
	}
	if int64(len(output)) > maximum {
		return errors.New("external driver stderr exceeds limit")
	}
	return nil
}
