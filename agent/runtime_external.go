package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/memory"
	"github.com/abcdlsj/mink/msg"
	rtsqlite "github.com/abcdlsj/mink/runtime/store/sqlite"
	"github.com/abcdlsj/mink/session"
)

type ExternalRuntime struct {
	driver ExternalDriver
	mem    *memory.Store
	rt     *rtsqlite.DB
	b      *bus.Bus
	sess   *session.Session

	source            string
	agentID           string
	workDir           string
	status            RuntimeStatus
	externalSessionID string
	inputTokens       int
	outputTokens      int
	mu                sync.Mutex

	cmd        *exec.Cmd
	cancel     context.CancelFunc
	lastOutput string
}

func externalSessionMetaKey(name string) string {
	return "external_session_" + strings.TrimSpace(name)
}

type ExternalDriver struct {
	Name        string
	Command     string
	BuildArgs   func(prompt, mcpConfigPath, workDir, sessionID string) []string
	ParseOutput func(line string) *RuntimeMessage
}

type ExternalRuntimeConfig struct {
	Driver  ExternalDriver
	Memory  *memory.Store
	RT      *rtsqlite.DB
	Bus     *bus.Bus
	Session *session.Session
	WorkDir string
}

func NewExternalRuntime(cfg ExternalRuntimeConfig) *ExternalRuntime {
	return &ExternalRuntime{
		driver:  cfg.Driver,
		mem:     cfg.Memory,
		rt:      cfg.RT,
		b:       cfg.Bus,
		sess:    cfg.Session,
		workDir: cfg.WorkDir,
		status:  RuntimeIdle,
	}
}

func (r *ExternalRuntime) Start(_ context.Context, cfg RuntimeConfig) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.source = cfg.Source
	r.agentID = cfg.AgentID
	if cfg.Session != nil {
		r.sess = cfg.Session
		r.externalSessionID = cfg.Session.MetaString(externalSessionMetaKey(r.driver.Name))
	}
	r.status = RuntimeIdle
	return nil
}

func (r *ExternalRuntime) Send(ctx context.Context, input string) (retErr error) {
	r.setStatus(RuntimeRunning)
	defer r.setStatus(RuntimeIdle)

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

	bridge := NewMCPBridge(MCPBridgeConfig{
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

	workDir := r.workDir
	if workDir == "" && r.rt != nil {
		workDir = r.rt.WorkspacePath()
	}
	if workDir == "" {
		workDir = "."
	}

	r.mu.Lock()
	externalSessionID := r.externalSessionID
	r.mu.Unlock()

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

	r.mu.Lock()
	r.cmd = nil
	r.lastOutput = output
	externalSessionID = r.externalSessionID
	r.mu.Unlock()

	if r.sess != nil && output != "" {
		r.sess.Add(msg.Message{Role: "assistant", Content: output})
	}
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

func (r *ExternalRuntime) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.status = RuntimeStopped
	if r.cancel != nil {
		r.cancel()
	}
	if r.cmd != nil && r.cmd.Process != nil {
		_ = r.cmd.Process.Signal(os.Interrupt)
	}
	return nil
}

func (r *ExternalRuntime) Status() RuntimeStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.status
}

func (r *ExternalRuntime) Session() *session.Session {
	return r.sess
}

func (r *ExternalRuntime) TokenUsage() msg.TokenUsage {
	r.mu.Lock()
	defer r.mu.Unlock()
	return msg.TokenUsage{
		Input:  r.inputTokens,
		Output: r.outputTokens,
		Total:  r.inputTokens + r.outputTokens,
	}
}

func (r *ExternalRuntime) Interrupt() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cmd != nil && r.cmd.Process != nil {
		_ = r.cmd.Process.Signal(os.Interrupt)
	}
}

func (r *ExternalRuntime) readOutput(ctx context.Context, stdout io.Reader) (string, bool) {
	var sb strings.Builder
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 256*1024), 10*1024*1024)
	sawStream := false

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return sb.String(), sawStream
		default:
		}

		line := scanner.Text()

		if r.driver.ParseOutput != nil {
			ev := r.driver.ParseOutput(line)
			if ev == nil {
				continue
			}
			r.handleRuntimeMessage(ev)
			switch ev.Type {
			case MsgStreamChunk:
				sawStream = true
				sb.WriteString(ev.Text)
			case MsgAssistantText:
				if sawStream {
					continue
				}
				if sb.Len() > 0 {
					sb.WriteString("\n")
				}
				sb.WriteString(ev.Text)
			case MsgTurnDone:
				if sb.Len() == 0 && ev.Text != "" {
					sb.WriteString(ev.Text)
				}
			case MsgError:
				if sb.Len() > 0 {
					sb.WriteString("\n")
				}
				sb.WriteString("error: ")
				sb.WriteString(ev.Text)
			}
			continue
		}

		sb.WriteString(line)
		sb.WriteString("\n")

		if r.b != nil {
			_ = r.b.Pub(bus.Msg{
				Type:    bus.TypeStreamChunk,
				From:    r.agentID,
				To:      r.source,
				Payload: line + "\n",
			})
		}
	}

	if r.b != nil {
		_ = r.b.Pub(bus.Msg{
			Type: bus.TypeStreamEnd,
			From: r.agentID,
			To:   r.source,
		})
	}

	return strings.TrimSpace(sb.String()), sawStream
}

func (r *ExternalRuntime) handleRuntimeMessage(m *RuntimeMessage) {
	if m.SessionID != "" || m.InputTokens > 0 || m.OutputTokens > 0 {
		r.mu.Lock()
		if m.SessionID != "" {
			r.externalSessionID = m.SessionID
		}
		r.inputTokens += m.InputTokens
		r.outputTokens += m.OutputTokens
		r.mu.Unlock()
	}
	if r.b == nil {
		return
	}
	switch m.Type {
	case MsgStreamChunk:
		_ = r.b.Pub(bus.Msg{
			Type:    bus.TypeStreamChunk,
			From:    r.agentID,
			To:      r.source,
			Payload: m.Text,
		})
	case MsgThinkingChunk:
		_ = r.b.Pub(bus.Msg{
			Type:    bus.TypeThinkingChunk,
			From:    r.agentID,
			To:      r.source,
			Payload: m.Text,
		})
	case MsgToolCall:
		_ = r.b.Pub(bus.Msg{
			Type: bus.TypeToolCall,
			From: r.agentID,
			To:   r.source,
			Payload: map[string]string{
				"id":   m.ToolID,
				"name": m.ToolName,
				"args": m.ToolArgs,
			},
		})
	case MsgToolResult:
		_ = r.b.Pub(bus.Msg{
			Type: bus.TypeToolResult,
			From: r.agentID,
			To:   r.source,
			Payload: map[string]string{
				"id":     m.ToolID,
				"result": m.Text,
			},
		})
	case MsgError:
		_ = r.b.Pub(bus.Msg{
			Type:    bus.TypeAssistant,
			From:    r.agentID,
			To:      r.source,
			Payload: "error: " + m.Text,
		})
	}
}

func (r *ExternalRuntime) writeMCPConfig(bridgeW *io.PipeWriter, clientR *io.PipeReader) (string, func(), error) {
	tmpDir, err := os.MkdirTemp("", "mink-mcp-*")
	if err != nil {
		return "", nil, err
	}

	sockPath := filepath.Join(tmpDir, "bridge.sock")

	listener, err := startBridgeRelay(sockPath, bridgeW, clientR)
	if err != nil {
		os.RemoveAll(tmpDir)
		return "", nil, err
	}

	minkBin, err := os.Executable()
	if err != nil {
		minkBin = "mink"
	}

	configPath := filepath.Join(tmpDir, "mcp.json")
	config := map[string]any{
		"mcpServers": map[string]any{
			"mink": map[string]any{
				"command": minkBin,
				"args":    []string{"mcp-bridge", "--sock", sockPath},
			},
		},
	}
	data, _ := json.MarshalIndent(config, "", "  ")
	if err := os.WriteFile(configPath, data, 0o644); err != nil {
		listener.Close()
		os.RemoveAll(tmpDir)
		return "", nil, err
	}

	cleanup := func() {
		listener.Close()
		os.RemoveAll(tmpDir)
	}
	return configPath, cleanup, nil
}

func (r *ExternalRuntime) setStatus(s RuntimeStatus) {
	r.mu.Lock()
	r.status = s
	r.mu.Unlock()
}
