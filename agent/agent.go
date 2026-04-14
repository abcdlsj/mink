package agent

import (
	"context"
	"sync"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/config"
	"github.com/abcdlsj/mink/hook"
	"github.com/abcdlsj/mink/llm"
	"github.com/abcdlsj/mink/memory"
	rtsqlite "github.com/abcdlsj/mink/runtime/store/sqlite"
	"github.com/abcdlsj/mink/session"
	"github.com/abcdlsj/mink/tool"
)

type Agent struct {
	id               string
	p                llm.Provider
	sel              *llm.Sel
	soulPath         string
	nextModel        string
	reg              *tool.Registry
	guard            tool.Guard
	extraTools       []tool.Tool
	session          *session.Session
	bus              *bus.Bus
	hooks            *hook.Manager
	prompt           string
	subAgent         bool
	cfg              config.Config
	stream           bool
	tok              *tokenEstimator
	base             tokenBaseline
	turnToolHistory  map[string]turnToolRecord
	turnStateVersion int
	rt               *rtsqlite.DB
	mem              *memory.Store
	interrupted      bool
	cancelFn         context.CancelFunc
	mu               sync.Mutex
}

type tokenBaseline struct {
	msgCount int
	total    int
	source   string
	valid    bool
}

type AgentDeps struct {
	Bus       *bus.Bus
	Provider  llm.Provider
	Sel       *llm.Sel
	Hooks     *hook.Manager
	ToolGuard tool.Guard
	CronTool  tool.Tool
	Prompt    string
	Config    config.Config
	RuntimeDB *rtsqlite.DB
	Memory    *memory.Store
}

func (d *AgentDeps) newAgent(id string, sess *session.Session, subAgent bool) *Agent {
	return New(id, d.Provider, sess,
		WithBus(d.Bus),
		WithSel(d.Sel),
		WithHooks(d.Hooks),
		WithToolGuard(d.ToolGuard),
		WithCronTool(d.CronTool),
		WithPrompt(d.Prompt),
		WithConfig(d.Config),
		WithSubAgent(subAgent),
		WithRuntimeDB(d.RuntimeDB),
		WithMemoryStore(d.Memory),
	)
}

type Option func(*Agent)

func WithHooks(h *hook.Manager) Option { return func(a *Agent) { a.hooks = h } }
func WithToolGuard(g tool.Guard) Option {
	return func(a *Agent) {
		a.guard = g
		if a.reg != nil {
			a.reg.SetGuard(g)
		}
	}
}
func WithPrompt(p string) Option           { return func(a *Agent) { a.prompt = p } }
func WithSubAgent(v bool) Option           { return func(a *Agent) { a.subAgent = v } }
func WithBus(b *bus.Bus) Option            { return func(a *Agent) { a.bus = b } }
func WithRegistry(r *tool.Registry) Option { return func(a *Agent) { a.reg = r } }
func WithProvider(p llm.Provider) Option   { return func(a *Agent) { a.p = p } }
func WithSoulPath(path string) Option      { return func(a *Agent) { a.soulPath = path } }
func WithConfig(c config.Config) Option {
	return func(a *Agent) {
		a.cfg = c
		a.stream = c.Stream
		a.ensureTokenEstimator()
	}
}
func WithStream(s bool) Option  { return func(a *Agent) { a.stream = s } }
func WithSel(s *llm.Sel) Option { return func(a *Agent) { a.sel = s } }
func WithRuntimeDB(db *rtsqlite.DB) Option {
	return func(a *Agent) { a.rt = db }
}
func WithMemoryStore(mem *memory.Store) Option {
	return func(a *Agent) { a.mem = mem }
}

func New(id string, p llm.Provider, s *session.Session, opts ...Option) *Agent {
	a := &Agent{
		id:        id,
		p:         p,
		session:   s,
		nextModel: "default",
		hooks:     hook.NewManager(),
	}
	for _, opt := range opts {
		opt(a)
	}
	if a.reg == nil {
		if a.sel != nil {
			a.reg = tool.NewRegistry(a.sel)
		} else {
			a.reg = tool.NewRegistry(nil)
		}
	}
	if a.mem != nil {
		a.reg.Register(tool.NewSearchMemory(a.mem, a.rt, id))
		a.reg.Register(tool.NewReadMemory(a.mem, a.rt, id))
		a.reg.Register(tool.NewWriteMemory(a.mem, a.rt, id))
	}
	if a.guard != nil {
		a.reg.SetGuard(a.guard)
	}
	for _, extra := range a.extraTools {
		a.reg.Register(extra)
	}
	if a.bus != nil {
		a.reg.Register(tool.NewSpawn(a.bus, id))
		a.reg.Register(tool.NewDelegate(a.bus, id))
		a.reg.Register(tool.NewDelegatePoll(a.bus, id))
		a.reg.Register(tool.NewTeamMention(a.bus, id))
		a.reg.Register(tool.NewTeamInvite(a.bus, id))
		a.reg.Register(tool.NewTeamSpawnSpecialist(a.bus, id))
		bg := tool.NewBackground(a.bus, id)
		if a.cfg.Timeout.Background > 0 {
			bg.SetTimeout(a.cfg.Timeout.Background)
		}
		a.reg.Register(bg)
	}
	a.reg.Register(tool.NewBraveSearch(a.cfg.Key("BRAVE_API_KEY")))
	a.ensureTokenEstimator()
	return a
}

func WithCronTool(t tool.Tool) Option {
	return func(a *Agent) {
		if t == nil {
			return
		}
		a.extraTools = append(a.extraTools, t)
		if a.reg != nil {
			a.reg.Register(t)
		}
	}
}

func WithExtraTool(t tool.Tool) Option {
	return func(a *Agent) {
		if t == nil {
			return
		}
		a.extraTools = append(a.extraTools, t)
		if a.reg != nil {
			a.reg.Register(t)
		}
	}
}

func (a *Agent) ID() string                { return a.id }
func (a *Agent) Session() *session.Session { return a.session }
func (a *Agent) Tools() *tool.Registry     { return a.reg }

func (a *Agent) Interrupt() {
	a.mu.Lock()
	a.interrupted = true
	if a.cancelFn != nil {
		a.cancelFn()
	}
	a.mu.Unlock()
}

func (a *Agent) IsInterrupted() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.interrupted
}

func (a *Agent) ResetInterrupt() {
	a.mu.Lock()
	a.interrupted = false
	a.cancelFn = nil
	a.mu.Unlock()
}
