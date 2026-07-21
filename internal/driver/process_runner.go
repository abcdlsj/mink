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
	"time"

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
		return fmt.Errorf("start external driver: %w", err)
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
	stdoutFinished := false
	stderrFinished := false
	waitFinished := false
	contextFinished := false
	for !stdoutFinished || !stderrFinished || !waitFinished {
		select {
		case stdoutErr = <-stdoutDone:
			stdoutFinished = true
			if stdoutErr != nil {
				cancel()
			}
		case stderrErr = <-stderrDone:
			stderrFinished = true
			if stderrErr != nil {
				cancel()
			}
		case waitErr = <-waitDone:
			waitFinished = true
		case <-runCtx.Done():
			if !contextFinished {
				contextErr = runCtx.Err()
				contextFinished = true
				cancel()
			}
		}
	}
	if stdoutErr != nil {
		return stdoutErr
	}
	if stderrErr != nil {
		return stderrErr
	}
	if contextErr != nil {
		return contextErr
	}
	if waitErr != nil {
		return errors.New("external driver process failed")
	}
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
