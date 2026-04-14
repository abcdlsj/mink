package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/abcdlsj/mink/agent"
	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/command"
	commandbuiltin "github.com/abcdlsj/mink/command/builtin"
	"github.com/abcdlsj/mink/config"
	mcron "github.com/abcdlsj/mink/cron"
	"github.com/abcdlsj/mink/hook"
	"github.com/abcdlsj/mink/llm"
	"github.com/abcdlsj/mink/memory"
	"github.com/abcdlsj/mink/msg"
	"github.com/abcdlsj/mink/platform"
	"github.com/abcdlsj/mink/platform/cliapp"
	"github.com/abcdlsj/mink/platform/telegrambot"
	"github.com/abcdlsj/mink/platform/webapp"
	rtsqlite "github.com/abcdlsj/mink/runtime/store/sqlite"
	"github.com/abcdlsj/mink/session"
)

var (
	ErrClosed         = errors.New("mink app is closed")
	ErrAPIKeyRequired = errors.New("need api key")

	Version   = "dev"
	Commit    = "unknown"
	BuildTime = "unknown"
)

type Options struct {
	Config    config.Config
	Bus       *bus.Bus
	Provider  llm.Provider
	Hooks     *hook.Manager
	Workspace string
}

type sourceState struct {
	teamID    string
	threadID  string
	section   string
	sessionID string
}

type App struct {
	cfg      config.Config
	bus      *bus.Bus
	p        llm.Provider
	sm       *session.Manager
	rt       *rtsqlite.DB
	mw       *memory.Watcher
	hooks    *hook.Manager
	cmdReg   *command.Registry
	router   *command.Router
	guard    *command.GuardMux
	sup      *agent.Supervisor
	disp     *agent.Dispatcher
	reg      *agent.Registry
	hb       *agent.HeartbeatManager
	cron     *mcron.Scheduler
	adapters []platform.Adapter

	cli       *cliapp.CLI
	web       *webapp.Web
	telegram  *telegrambot.Telegram
	workspace string

	sources map[string]*sourceState

	mu      sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
	started bool
	closed  bool
}

func New(opts Options) (*App, error) {
	deps, err := resolveRuntimeDeps(opts)
	if err != nil {
		return nil, err
	}

	cmdReg, router, guard := buildCommandInfra(deps.workspace)
	sm, sup, disp, reg, cronSched, err := buildAgentInfra(deps, guard)
	if err != nil {
		return nil, err
	}
	registerRuntimeCommands(cmdReg, deps.bus, sm, disp, workspacePlatformSource("cli", deps.workspace), deps.memory, deps.runtimeDB)

	app := &App{
		cfg:       deps.cfg,
		bus:       deps.bus,
		p:         deps.provider,
		sm:        sm,
		rt:        deps.runtimeDB,
		mw:        deps.memoryWatcher,
		hooks:     deps.hooks,
		cmdReg:    cmdReg,
		router:    router,
		guard:     guard,
		sup:       sup,
		disp:      disp,
		reg:       reg,
		hb:        agent.NewHeartbeatManager(reg, deps.bus),
		cron:      cronSched,
		workspace: deps.workspace,

		sources: make(map[string]*sourceState),
	}

	cmdReg.Register(commandbuiltin.NewModelsCmd(app.modelsInfo))
	cmdReg.Register(commandbuiltin.NewModelCmd(app.switchModel))
	cmdReg.Register(commandbuiltin.NewAgentsCmd(app.agentsInfo))
	cmdReg.Register(command.NewFuncCmd("team", "manage teams (!team list|create|open|home|invite)", app.runTeamCommand))
	cmdReg.Register(command.NewFuncCmd("thread", "manage team threads (!thread list|new|open)", app.runThreadCommand))

	return app, nil
}

func (a *App) Start(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.closed {
		return ErrClosed
	}
	if a.started {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	a.ctx, a.cancel = context.WithCancel(ctx)
	if a.rt != nil {
		if err := a.rt.Recover(a.ctx); err != nil {
			a.cancel()
			a.ctx = nil
			a.cancel = nil
			return err
		}
	}
	if a.mw != nil {
		if err := a.mw.Start(a.ctx); err != nil {
			a.cancel()
			a.ctx = nil
			a.cancel = nil
			return err
		}
	}
	if a.sup != nil {
		if err := a.sup.Start(a.ctx); err != nil {
			a.cancel()
			a.ctx = nil
			a.cancel = nil
			return err
		}
	}
	a.disp.Start(a.ctx)
	_ = a.cron.Start(a.ctx)
	if a.hb != nil {
		_ = a.hb.Start(a.ctx)
	}
	a.started = true
	return nil
}

func (a *App) Commands() *command.Registry {
	return a.cmdReg
}

func (a *App) Hooks() *hook.Manager {
	return a.hooks
}

func (a *App) Submit(src, input string) error {
	src = strings.TrimSpace(src)
	if src == "" {
		return fmt.Errorf("source is required")
	}

	return a.bus.Pub(bus.Msg{
		Type:    bus.TypeUserInput,
		From:    src,
		To:      bus.AddrAgentMain,
		Payload: input,
	})
}

func (a *App) ResetSession(src string) error {
	src = strings.TrimSpace(src)
	if src == "" {
		return fmt.Errorf("source is required")
	}

	return a.bus.Pub(bus.Msg{
		Type:    bus.TypeSessionReset,
		From:    src,
		To:      bus.AddrAgentMain,
		Payload: src,
	})
}

func (a *App) CompactSession(src, note string) error {
	src = strings.TrimSpace(src)
	if src == "" {
		return fmt.Errorf("source is required")
	}

	return a.bus.Pub(bus.Msg{
		Type:    bus.TypeSessionCompact,
		From:    src,
		To:      bus.AddrAgentMain,
		Payload: note,
	})
}

func (a *App) Usage(src string) (msg.TokenUsage, bool) {
	return a.disp.Usage(src)
}

func (a *App) Subscribe(msgType string, ch chan bus.Msg) {
	a.bus.Subscribe(msgType, ch)
}

func (a *App) Unsubscribe(msgType string, ch chan bus.Msg) {
	a.bus.Unsubscribe(msgType, ch)
}

func (a *App) cliStatus() func() cliapp.StatusInfo {
	home, _ := os.UserHomeDir()
	pwd, _ := os.Getwd()
	ws := pwd
	if home != "" && strings.HasPrefix(ws, home) {
		ws = "~" + ws[len(home):]
	}
	cliSource := a.cliSource()

	return func() cliapp.StatusInfo {
		model := a.disp.ModelDisplayName()

		u, _ := a.disp.Usage(cliSource)

		sessID, _ := a.sm.CurrentID(cliSource)

		var agents []cliapp.AgentInfo
		if a.reg != nil {
			states := a.reg.All()
			sort.Slice(states, func(i, j int) bool {
				if states[i].Status != states[j].Status {
					return states[i].Status < states[j].Status
				}
				left := states[i].Descriptor.Name
				if left == "" {
					left = states[i].Descriptor.ID
				}
				right := states[j].Descriptor.Name
				if right == "" {
					right = states[j].Descriptor.ID
				}
				return left < right
			})
			agents = make([]cliapp.AgentInfo, 0, len(states))
			for _, state := range states {
				agents = append(agents, cliapp.AgentInfo{
					ID:     state.Descriptor.ID,
					Name:   state.Descriptor.Name,
					Status: string(state.Status),
					Runs:   len(state.ActiveRuns),
					Caps:   state.Descriptor.Capabilities,
				})
			}
		}

		return cliapp.StatusInfo{
			Model:     model,
			TokenIn:   u.Input,
			TokenOut:  u.Output,
			Workspace: ws,
			Session:   sessID,
			Agents:    agents,
			Team:      a.teamStatusForSource(context.Background(), cliSource),
		}
	}
}

func (a *App) cliSessionMessages(source string) func() []msg.Message {
	return func() []msg.Message {
		if source == "" {
			return nil
		}
		sess, err := a.sm.Current(source)
		if err != nil || sess == nil {
			return nil
		}
		return sess.View().Messages
	}
}

func (a *App) modelsInfo() commandbuiltin.ModelInfo {
	a.mu.Lock()
	defer a.mu.Unlock()
	return commandbuiltin.ModelInfo{Models: a.cfg.Models, Active: a.cfg.ActiveModel}
}

func (a *App) agentsInfo() string {
	if a.reg == nil {
		return "no agents configured"
	}
	states := a.reg.All()
	if len(states) == 0 {
		return "no agents registered"
	}
	var b strings.Builder
	b.WriteString("Agents:\n")
	for _, s := range states {
		fmt.Fprintf(&b, "  %s (%s) [%s] caps=%v runs=%d\n",
			s.Descriptor.ID, s.Descriptor.Name, s.Status,
			s.Descriptor.Capabilities, len(s.ActiveRuns))
	}
	return b.String()
}

func (a *App) switchModel(name string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !config.ResolveModel(&a.cfg, name) {
		return fmt.Errorf("model %q not found", name)
	}

	p, err := newProviderFromModel(a.cfg.Active)
	if err != nil {
		return fmt.Errorf("create provider: %w", err)
	}
	sel := newSelector(a.cfg, p)

	a.p = p
	a.disp.SetLLM(p, sel)
	a.disp.SetConfig(a.cfg)
	a.disp.ResetAgents()

	return config.SaveActiveModel(name)
}
