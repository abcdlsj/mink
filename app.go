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
	cfg := normalizeConfig(opts.Config)

	b := opts.Bus
	if b == nil {
		b = bus.New()
	}

	p := opts.Provider
	if p == nil {
		if cfg.Active.APIKey == "" {
			return nil, ErrAPIKeyRequired
		}

		var err error
		p, err = llm.NewProvider(llm.Config{
			Provider:            cfg.Active.Provider,
			APIKey:              cfg.Active.APIKey,
			BaseURL:             cfg.Active.BaseURL,
			Model:               cfg.Active.Model,
			Headers:             cfg.Active.Headers,
			OpenRouterReasoning: cfg.Active.OpenRouterReasoning,
		})
		if err != nil {
			return nil, err
		}
	}

	sessionDir := opts.SessionDir
	if sessionDir == "" {
		sessionDir = defaultSessionDir()
	}
	store := opts.SessionStore
	if store == nil {
		store = session.NewFileStore(sessionDir)
	}

	hooks := opts.Hooks
	if hooks == nil {
		hooks = hook.NewManager()
	}

	workspace := opts.Workspace
	if workspace == "" {
		workspace, _ = os.Getwd()
	}

	sm := session.NewManager(store, b)
	cmdReg := command.NewRegistry()
	cmdReg.Register(command.NewHelpCmd(cmdReg))
	cmdReg.Register(command.NewCompactCmd(b))

	perms := tool.NewPermissions(filepath.Join(workspace, ".mink", "permissions.json"))

	router := command.NewRouter(cmdReg)
	guard := command.NewGuardMux(perms)
	router.SetGuard(guard)

	deps := agent.AgentDeps{
		Bus:        b,
		Provider:   p,
		Hooks:      hooks,
		ToolGuard:  guard,
		Prompt:     cfg.CustomPrompt,
		Config:     cfg,
		SessionDir: sessionDir,
	}

	sup := agent.NewSupervisor(deps, sm)
	var sl *skill.Loader
	if workspace != "" {
		sl = skill.NewLoader(workspace)
	}
	disp := agent.NewDispatcher(deps, sm, sl)

	cmdReg.Register(command.NewTokensCmd(disp.Usage))
	cmdReg.Register(command.NewSessionCmd(sm))

	app := &App{
		cfg:    cfg,
		bus:    b,
		p:      p,
		sm:     sm,
		hooks:  hooks,
		cmdReg: cmdReg,
		router: router,
		guard:  guard,
		sup:    sup,
		disp:   disp,
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
	a.disp.Start(a.ctx)
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
	if a.telegram != nil {
		a.mu.Unlock()
		return nil
	}
	runCtx := a.ctx
	a.mu.Unlock()

	tg := platform.NewTelegram(token, a.bus)
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

	for _, ad := range adapters {
		_ = ad.Stop()
	}

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

func (a *App) ReloadConfig(cfg config.Config) {
	a.mu.Lock()
	a.cfg = cfg
	a.mu.Unlock()
	a.disp.SetConfig(cfg)
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

	p, err := llm.NewProvider(llm.Config{
		Provider:            a.cfg.Active.Provider,
		APIKey:              a.cfg.Active.APIKey,
		BaseURL:             a.cfg.Active.BaseURL,
		Model:               a.cfg.Active.Model,
		Headers:             a.cfg.Active.Headers,
		OpenRouterReasoning: a.cfg.Active.OpenRouterReasoning,
	})
	if err != nil {
		return fmt.Errorf("create provider: %w", err)
	}

	a.p = p
	a.disp.SetProvider(p)
	a.disp.SetConfig(a.cfg)
	a.disp.ResetAgents()

	return config.SaveActiveModel(name)
}

func normalizeConfig(cfg config.Config) config.Config {
	if cfg.Mode == "" {
		cfg.Mode = "tui"
	}
	if cfg.Timeout.Tool == 0 {
		cfg.Timeout.Tool = 60
	}
	if cfg.Timeout.Agent == 0 {
		cfg.Timeout.Agent = 600
	}
	if cfg.Timeout.Background == 0 {
		cfg.Timeout.Background = 1800
	}
	if cfg.Timeout.LLM == 0 {
		cfg.Timeout.LLM = 120
	}
	if cfg.MaxSteps == 0 {
		cfg.MaxSteps = 100
	}
	if cfg.Compact.TriggerTokens == 0 {
		cfg.Compact.TriggerTokens = 12000
	}
	if cfg.Compact.TriggerMessages == 0 {
		cfg.Compact.TriggerMessages = 80
	}
	if cfg.Compact.KeepRecentMessages == 0 {
		cfg.Compact.KeepRecentMessages = 20
	}
	return cfg
}

func defaultSessionDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".mink", "sessions")
	}
	return filepath.Join(home, ".mink", "sessions")
}
