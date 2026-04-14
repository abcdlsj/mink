package external

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"

	agrt "github.com/abcdlsj/mink/agent/runtime"
	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/msg"
)

func (r *ExternalRuntime) Start(_ context.Context, cfg agrt.Config) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.source = cfg.Source
	r.agentID = cfg.AgentID
	if cfg.Session != nil {
		r.sess = cfg.Session
		r.externalSessionID = cfg.Session.MetaString(externalSessionMetaKey(r.driver.Name))
	}
	r.status = agrt.Idle
	return nil
}

func (r *ExternalRuntime) Send(ctx context.Context, input string) (retErr error) {
	r.setStatus(agrt.Running)
	defer r.setStatus(agrt.Idle)

	ctx, cancel := context.WithCancel(ctx)
	r.mu.Lock()
	r.cancel = cancel
	r.mu.Unlock()
	defer cancel()
	defer func() {
		if r.sess == nil {
			return
		}
		if err := r.sess.Flush(); err != nil {
			if retErr == nil {
				retErr = err
				return
			}
			retErr = fmt.Errorf("%w; flush: %v", retErr, err)
		}
	}()

	if r.sess != nil {
		r.sess.Add(msg.Message{Role: "user", Content: input})
	}

	bridgeR, bridgeW := io.Pipe()
	clientR, clientW := io.Pipe()

	bridge := NewBridge(BridgeConfig{
		Memory:  r.mem,
		RT:      r.rt,
		Bus:     r.b,
		AgentID: r.agentID,
		Source:  r.source,
	})

	bridgeCtx, bridgeCancel := context.WithCancel(ctx)
	defer bridgeCancel()

	var bridgeErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer clientW.Close()
		bridgeErr = bridge.Serve(bridgeCtx, bridgeR, clientW)
	}()

	mcpConfig, cleanup, err := r.writeMCPConfig(bridgeW, clientR)
	if err != nil {
		bridgeCancel()
		return fmt.Errorf("write mcp config: %w", err)
	}
	defer cleanup()

	workDir := r.resolveWorkDir()
	externalSessionID := r.currentExternalSessionID()

	args := r.driver.BuildArgs(input, mcpConfig, workDir, externalSessionID)
	cmd := exec.CommandContext(ctx, r.driver.Command, args...)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), "MINK_AGENT_ID="+r.agentID, "MINK_SOURCE="+r.source)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		bridgeCancel()
		return fmt.Errorf("stdout pipe: %w", err)
	}
	cmd.Stderr = os.Stderr
	if r.driver.StdinPrompt {
		stdinPipe, err := cmd.StdinPipe()
		if err != nil {
			bridgeCancel()
			return fmt.Errorf("stdin pipe: %w", err)
		}
		go func() {
			_, _ = io.WriteString(stdinPipe, input)
			stdinPipe.Close()
		}()
	} else {
		cmd.Stdin = nil
	}

	r.mu.Lock()
	r.cmd = cmd
	r.mu.Unlock()

	if err := cmd.Start(); err != nil {
		bridgeCancel()
		return fmt.Errorf("start %s: %w", r.driver.Name, err)
	}

	output, streamed := r.readOutput(ctx, stdout)

	err = cmd.Wait()
	bridgeCancel()
	wg.Wait()

	externalSessionID = r.finishRun(output)

	if r.sess != nil && externalSessionID != "" {
		if err := r.sess.SetMetaString(externalSessionMetaKey(r.driver.Name), externalSessionID); err != nil && retErr == nil {
			retErr = err
		}
	}

	if r.b != nil && output != "" && !streamed {
		_ = r.b.Pub(bus.Msg{
			Type:    bus.TypeAssistant,
			From:    r.agentID,
			To:      r.source,
			Payload: output,
		})
	}

	if err != nil && ctx.Err() == nil {
		return fmt.Errorf("%s exited: %w", r.driver.Name, err)
	}
	if bridgeErr != nil && !errors.Is(bridgeErr, context.Canceled) {
		return fmt.Errorf("mcp bridge: %w", bridgeErr)
	}
	return nil
}

func (r *ExternalRuntime) SendSystem(ctx context.Context, input string) error {
	return r.Send(ctx, input)
}

func (r *ExternalRuntime) resolveWorkDir() string {
	workDir := r.workDir
	if workDir == "" && r.rt != nil {
		workDir = r.rt.WorkspacePath()
	}
	if workDir == "" {
		return "."
	}
	return workDir
}

func (r *ExternalRuntime) currentExternalSessionID() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.externalSessionID
}

func (r *ExternalRuntime) finishRun(output string) string {
	r.mu.Lock()
	r.cmd = nil
	r.lastOutput = output
	externalSessionID := r.externalSessionID
	r.mu.Unlock()

	if r.sess != nil && output != "" {
		r.sess.Add(msg.Message{Role: "assistant", Content: output})
	}

	return externalSessionID
}
