package agent

import (
	"context"
	"os"
	"os/exec"
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
	StdinPrompt bool // if true, prompt is piped via stdin instead of as a CLI argument
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

func (r *ExternalRuntime) setStatus(s RuntimeStatus) {
	r.mu.Lock()
	r.status = s
	r.mu.Unlock()
}
