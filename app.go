package mink

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/abcdlsj/mink/agent"
	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/command"
	"github.com/abcdlsj/mink/config"
	mcron "github.com/abcdlsj/mink/cron"
	"github.com/abcdlsj/mink/hook"
	"github.com/abcdlsj/mink/llm"
	"github.com/abcdlsj/mink/msg"
	"github.com/abcdlsj/mink/platform"
	"github.com/abcdlsj/mink/session"
	"github.com/abcdlsj/mink/skill"
	"github.com/abcdlsj/mink/tool"
)

var (
	ErrClosed         = errors.New("mink app is closed")
	ErrAPIKeyRequired = errors.New("need api key")

	Version   = "dev"
	Commit    = "unknown"
	BuildTime = "unknown"
)

type Options struct {
	Config       config.Config
	Bus          *bus.Bus
	Provider     llm.Provider
	SessionStore session.Store
	SessionDir   string
	Hooks        *hook.Manager
	Workspace    string
}

type App struct {
	cfg      config.Config
	bus      *bus.Bus
	p        llm.Provider
	sm       *session.Manager
	hooks    *hook.Manager
	cmdReg   *command.Registry
	router   *command.Router
	guard    *command.GuardMux
	sup      *agent.Supervisor
	disp     *agent.Dispatcher
	cron     *mcron.Scheduler
	adapters []platform.Adapter

	cli      *platform.CLI
	telegram *platform.Telegram

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
	sm, sup, disp, cronSched := buildAgentInfra(deps, guard)
	registerRuntimeCommands(cmdReg, deps.bus, sm, disp, deps.sessionDir)

	app := &App{
		cfg:    deps.cfg,
		bus:    deps.bus,
		p:      deps.provider,
		sm:     sm,
		hooks:  deps.hooks,
		cmdReg: cmdReg,
		router: router,
		guard:  guard,
		sup:    sup,
		disp:   disp,
		cron:   cronSched,
	}

	cmdReg.Register(command.NewModelsCmd(app.modelsInfo))
	cmdReg.Register(command.NewModelCmd(app.switchModel))

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
	a.started = true
	return nil
}

func (a *App) StartCLI(ctx context.Context) error {
	if err := a.Start(ctx); err != nil {
		return err
	}

	a.mu.Lock()
	if a.cli != nil {
		a.mu.Unlock()
		return nil
	}
	runCtx := a.ctx
	a.mu.Unlock()

	cli := platform.NewCLI(a.bus, a.router, a.hooks, a.cliStatus())
	if err := cli.Start(runCtx); err != nil {
		return err
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		_ = cli.Stop()
		return ErrClosed
	}
	if a.cli != nil {
		_ = cli.Stop()
		return nil
	}

	a.cli = cli
	a.adapters = append(a.adapters, cli)
	a.guard.Register(bus.AddrPlatformCLI, cli)
	return nil
}

func (a *App) RunCLI(ctx context.Context) error {
	if err := a.StartCLI(ctx); err != nil {
		return err
	}

	a.mu.Lock()
	cli := a.cli
	a.mu.Unlock()
	if cli == nil {
		return fmt.Errorf("cli adapter not initialized")
	}

	return cli.Run()
}

func (a *App) StartTelegram(ctx context.Context, token string) error {
	if token == "" {
		token = a.cfg.Key("TELEGRAM_TOKEN")
	}
	if token == "" {
		return fmt.Errorf("tg mode need telegram token")
	}

	a.mu.Lock()
	if a.cfg.Mode != "tg" {
		a.cfg.Mode = "tg"
		a.disp.SetConfig(a.cfg)
	}
	a.mu.Unlock()

	if err := a.Start(ctx); err != nil {
		return err
	}

	a.mu.Lock()
	if a.telegram != nil && a.telegram.Token() == token {
		a.mu.Unlock()
		return nil
	}
	oldTG := a.telegram
	if oldTG != nil {
		a.telegram = nil
		a.adapters = removeAdapter(a.adapters, oldTG.ID())
		a.guard.Unregister("telegram:")
	}
	runCtx := a.ctx
	a.mu.Unlock()

	if oldTG != nil {
		_ = oldTG.Stop()
	}

	tg := platform.NewTelegram(token, a.bus, platform.TelegramOptions{
		MentionMode:  a.cfg.TelegramMentionMode,
		SessionScope: a.cfg.TelegramSessionScope,
	})
	if err := tg.Start(runCtx); err != nil {
		return err
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		_ = tg.Stop()
		return ErrClosed
	}
	if a.telegram != nil {
		_ = tg.Stop()
		return nil
	}

	a.telegram = tg
	a.adapters = append(a.adapters, tg)
	a.guard.Register("telegram:", tg)
	return nil
}

func (a *App) Close() error {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil
	}
	a.closed = true

	cancel := a.cancel
	adapters := append([]platform.Adapter(nil), a.adapters...)
	a.adapters = nil
	a.cli = nil
	a.telegram = nil
	a.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if a.sup != nil {
		_ = a.sup.Stop()
	}

	for _, ad := range adapters {
		_ = ad.Stop()
	}

	return nil
}

func removeAdapter(adapters []platform.Adapter, id string) []platform.Adapter {
	if len(adapters) == 0 {
		return nil
	}
	out := adapters[:0]
	for _, ad := range adapters {
		if ad == nil || ad.ID() == id {
			continue
		}
		out = append(out, ad)
	}
	return out
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

func (a *App) cliStatus() func() platform.StatusInfo {
	home, _ := os.UserHomeDir()
	pwd, _ := os.Getwd()
	ws := pwd
	if home != "" && strings.HasPrefix(ws, home) {
		ws = "~" + ws[len(home):]
	}

	return func() platform.StatusInfo {
		a.mu.Lock()
		model := a.cfg.ActiveModel
		a.mu.Unlock()

		u, _ := a.disp.Usage(bus.AddrPlatformCLI)

		sessID := ""
		if ag := a.disp.Agent(bus.AddrPlatformCLI); ag != nil {
			sessID = ag.Session().ID()
		}

		return platform.StatusInfo{
			Model:     model,
			TokenIn:   u.Input,
			TokenOut:  u.Output,
			Workspace: ws,
			Session:   sessID,
		}
	}
}

func (a *App) modelsInfo() command.ModelInfo {
	a.mu.Lock()
	defer a.mu.Unlock()
	return command.ModelInfo{Models: a.cfg.Models, Active: a.cfg.ActiveModel}
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

func defaultSessionDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".mink", "sessions")
	}
	return filepath.Join(home, ".mink", "sessions")
}

func newProviderFromModel(model config.ModelConfig) (llm.Provider, error) {
	if model.APIKey == "" {
		return nil, ErrAPIKeyRequired
	}
	return llm.NewProvider(llm.Config{
		Provider:  model.Provider,
		APIKey:    model.APIKey,
		BaseURL:   model.BaseURL,
		Model:     model.Model,
		Headers:   model.Headers,
		Reasoning: model.Reasoning,
	})
}

func newSelector(cfg config.Config, primary llm.Provider) *llm.Sel {
	if cfg.CheapModel == "" {
		return nil
	}
	cheapCfg := cfg
	if !cheapCfg.ResolveCheapModel() {
		return nil
	}
	cheap, err := newProviderFromModel(cheapCfg.Active)
	if err != nil {
		return nil
	}
	return llm.NewSel(primary, cheap)
}

type runtimeDeps struct {
	cfg        config.Config
	bus        *bus.Bus
	provider   llm.Provider
	selector   *llm.Sel
	sessionDir string
	workspace  string
	store      session.Store
	hooks      *hook.Manager
}

func resolveRuntimeDeps(opts Options) (runtimeDeps, error) {
	deps := runtimeDeps{cfg: opts.Config}
	deps.cfg.Normalize()

	deps.bus = opts.Bus
	if deps.bus == nil {
		deps.bus = bus.New()
	}

	deps.provider = opts.Provider
	if deps.provider == nil {
		provider, err := newProviderFromModel(deps.cfg.Active)
		if err != nil {
			return runtimeDeps{}, err
		}
		deps.provider = provider
		deps.selector = newSelector(deps.cfg, deps.provider)
	}

	deps.sessionDir = opts.SessionDir
	if deps.sessionDir == "" {
		deps.sessionDir = defaultSessionDir()
	}

	deps.store = opts.SessionStore
	if deps.store == nil {
		deps.store = session.NewFileStore(deps.sessionDir)
	}

	deps.hooks = opts.Hooks
	if deps.hooks == nil {
		deps.hooks = hook.NewManager()
	}

	deps.workspace = opts.Workspace
	if deps.workspace == "" {
		deps.workspace, _ = os.Getwd()
	}

	return deps, nil
}

func buildCommandInfra(workspace string) (*command.Registry, *command.Router, *command.GuardMux) {
	cmdReg := command.NewRegistry()
	cmdReg.Register(command.NewHelpCmd(cmdReg))

	perms := tool.NewPermissions(filepath.Join(workspace, ".mink", "permissions.json"))
	router := command.NewRouter(cmdReg)
	guard := command.NewGuardMux(perms)
	router.SetGuard(guard)

	return cmdReg, router, guard
}

func buildAgentInfra(deps runtimeDeps, guard *command.GuardMux) (*session.Manager, *agent.Supervisor, *agent.Dispatcher, *mcron.Scheduler) {
	sm := session.NewManager(deps.store, deps.bus)
	cronSched := mcron.NewScheduler(config.CronPath(), deps.bus)

	agentDeps := agent.AgentDeps{
		Bus:        deps.bus,
		Provider:   deps.provider,
		Sel:        deps.selector,
		Hooks:      deps.hooks,
		ToolGuard:  guard,
		Prompt:     deps.cfg.CustomPrompt,
		Config:     deps.cfg,
		SessionDir: deps.sessionDir,
	}
	agentDeps.CronTool = tool.NewCron(config.CronPath(), cronSched)

	sup := agent.NewSupervisor(agentDeps, sm)

	var skillLoader *skill.Loader
	if deps.workspace != "" {
		skillLoader = skill.NewLoader(deps.workspace)
	}

	disp := agent.NewDispatcher(agentDeps, sm, skillLoader)

	return sm, sup, disp, cronSched
}

func registerRuntimeCommands(cmdReg *command.Registry, eventBus *bus.Bus, sm *session.Manager, disp *agent.Dispatcher, sessionDir string) {
	if compact := command.NewCompactCmd(eventBus); compact != nil {
		cmdReg.Register(compact)
	}
	cmdReg.Register(command.NewReplayCmd(sm, sessionDir))
	cmdReg.Register(command.NewToolsCmd(func() []tool.Tool {
		if a := disp.Agent(bus.AddrPlatformCLI); a != nil && a.Tools() != nil {
			return a.Tools().All()
		}
		reg := tool.NewRegistry(nil)
		return reg.All()
	}))
	cmdReg.Register(command.NewTokensCmd(disp.Usage))
	cmdReg.Register(command.NewSessionCmd(sm, disp))
}
