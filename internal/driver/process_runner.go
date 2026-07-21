package driver

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type ProcessRunner struct {
	Path             string
	Args             []string
	Env              []string
	Timeout          time.Duration
	TerminationGrace time.Duration
	MaxOutputBytes   int64
}

func (r ProcessRunner) Run(ctx context.Context, input []byte, emit func([]byte) error) error {
	if err := r.validate(); err != nil {
		return err
	}
	if emit == nil {
		return errors.New("external driver line handler is required")
	}
	runCtx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()
	command := exec.Command(r.Path, r.Args...)
	command.Args = append([]string{r.Path}, r.Args...)
	command.Env = append([]string{}, r.Env...)
	command.Stdin = bytes.NewReader(input)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return fmt.Errorf("create external driver stdout: %w", err)
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return fmt.Errorf("create external driver stderr: %w", err)
	}
	if err := command.Start(); err != nil {
		return fmt.Errorf("start external driver: %w", err)
	}
	stdoutDone := make(chan error, 1)
	go func() { stdoutDone <- emitJSONLLines(stdout, r.MaxOutputBytes, emit) }()
	stderrDone := make(chan error, 1)
	go func() { stderrDone <- drainBounded(stderr, r.MaxOutputBytes) }()
	waitDone := make(chan error, 1)
	go func() { waitDone <- command.Wait() }()

	var stdoutErr, stderrErr, waitErr error
	stdoutFinished := false
	stderrFinished := false
	waitFinished := false
	for !stdoutFinished || !stderrFinished || !waitFinished {
		select {
		case stdoutErr = <-stdoutDone:
			stdoutFinished = true
			if stdoutErr != nil {
				r.stop(command, waitDone, &waitFinished, &waitErr)
				return stdoutErr
			}
		case stderrErr = <-stderrDone:
			stderrFinished = true
			if stderrErr != nil {
				r.stop(command, waitDone, &waitFinished, &waitErr)
				return stderrErr
			}
		case waitErr = <-waitDone:
			waitFinished = true
		case <-runCtx.Done():
			r.stop(command, waitDone, &waitFinished, &waitErr)
			return runCtx.Err()
		}
	}
	if waitErr != nil {
		return errors.New("external driver process failed")
	}
	return nil
}

func (r ProcessRunner) stop(command *exec.Cmd, waitDone <-chan error, waitFinished *bool, waitErr *error) {
	if !*waitFinished && command.Process != nil {
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGTERM)
		timer := time.NewTimer(r.TerminationGrace)
		select {
		case *waitErr = <-waitDone:
			*waitFinished = true
		case <-timer.C:
			_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
			*waitErr = <-waitDone
			*waitFinished = true
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}
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
	for _, argument := range r.Args {
		if strings.IndexByte(argument, 0) >= 0 {
			return errors.New("external driver argument contains NUL")
		}
	}
	seen := make(map[string]struct{}, len(r.Env))
	for _, value := range r.Env {
		key, _, found := strings.Cut(value, "=")
		if !found || key == "" || strings.IndexByte(key, 0) >= 0 || strings.IndexByte(value, 0) >= 0 {
			return errors.New("external driver environment is invalid")
		}
		if _, duplicate := seen[key]; duplicate {
			return errors.New("external driver environment contains duplicate key")
		}
		seen[key] = struct{}{}
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
