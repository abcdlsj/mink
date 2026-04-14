package external

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"

	agrt "github.com/abcdlsj/mink/agent/runtime"
	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/memory"
	"github.com/abcdlsj/mink/msg"
	rtsqlite "github.com/abcdlsj/mink/runtime/store/sqlite"
	"github.com/abcdlsj/mink/session"
)

type ExternalRuntime struct {
	driver agrt.Driver
	mem    *memory.Store
	rt     *rtsqlite.DB
	b      *bus.Bus
	sess   *session.Session

	source            string
	agentID           string
	workDir           string
	status            agrt.Status
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

type Config struct {
	Driver  agrt.Driver
	Memory  *memory.Store
	RT      *rtsqlite.DB
	Bus     *bus.Bus
	Session *session.Session
	WorkDir string
}

func New(cfg Config) *ExternalRuntime {
	return &ExternalRuntime{
		driver:  cfg.Driver,
		mem:     cfg.Memory,
		rt:      cfg.RT,
		b:       cfg.Bus,
		sess:    cfg.Session,
		workDir: cfg.WorkDir,
		status:  agrt.Idle,
	}
}

func (r *ExternalRuntime) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.status = agrt.Stopped
	if r.cancel != nil {
		r.cancel()
	}
	if r.cmd != nil && r.cmd.Process != nil {
		_ = r.cmd.Process.Signal(os.Interrupt)
	}
	return nil
}

func (r *ExternalRuntime) Status() agrt.Status {
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

func (r *ExternalRuntime) setStatus(s agrt.Status) {
	r.mu.Lock()
	r.status = s
	r.mu.Unlock()
}
