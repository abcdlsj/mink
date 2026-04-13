package mink

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/abcdlsj/mink/agent"
	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/command"
	"github.com/abcdlsj/mink/config"
	mcron "github.com/abcdlsj/mink/cron"
	"github.com/abcdlsj/mink/hook"
	"github.com/abcdlsj/mink/llm"
	"github.com/abcdlsj/mink/memory"
	"github.com/abcdlsj/mink/msg"
	"github.com/abcdlsj/mink/platform"
	rtsqlite "github.com/abcdlsj/mink/runtime/store/sqlite"
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

	cli        *platform.CLI
	web        *platform.Web
	telegram   *platform.Telegram
	sessionDir string
	workspace  string

	activeTeams        map[string]string
	activeThreads      map[string]string
	activeSections     map[string]string
	activeMainSessions map[string]string

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
	registerRuntimeCommands(cmdReg, deps.bus, sm, disp, deps.sessionDir, workspacePlatformSource("cli", deps.workspace), deps.memory, deps.runtimeDB)

	app := &App{
		cfg:        deps.cfg,
		bus:        deps.bus,
		p:          deps.provider,
		sm:         sm,
		rt:         deps.runtimeDB,
		mw:         deps.memoryWatcher,
		hooks:      deps.hooks,
		cmdReg:     cmdReg,
		router:     router,
		guard:      guard,
		sup:        sup,
		disp:       disp,
		reg:        reg,
		hb:         agent.NewHeartbeatManager(reg, deps.bus),
		cron:       cronSched,
		sessionDir: deps.sessionDir,
		workspace:  deps.workspace,

		activeTeams:        make(map[string]string),
		activeThreads:      make(map[string]string),
		activeSections:     make(map[string]string),
		activeMainSessions: make(map[string]string),
	}

	cmdReg.Register(command.NewModelsCmd(app.modelsInfo))
	cmdReg.Register(command.NewModelCmd(app.switchModel))
	cmdReg.Register(command.NewAgentsCmd(app.agentsInfo))
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

	cliSource := a.cliSource()
	if _, err := a.prepareFreshSource(runCtx, cliSource); err != nil {
		return err
	}

	cli := platform.NewCLI(a.bus, a.router, a.hooks, a.cliStatus(), a.cliSessionMessages(cliSource), cliSource)
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
	a.guard.Register(cliSource, cli)
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

func (a *App) StartWeb(ctx context.Context, addr string) error {
	if err := a.Start(ctx); err != nil {
		return err
	}

	a.mu.Lock()
	if a.web != nil {
		a.mu.Unlock()
		return nil
	}
	runCtx := a.ctx
	a.mu.Unlock()

	webSource := workspacePlatformSource("web", a.workspace)
	sessionID, err := a.prepareFreshSource(runCtx, webSource)
	if err != nil {
		return err
	}
	_ = a.sm.Update(sessionID, func(s *session.Session) {
		s.SetKind("main")
		s.SetStatus("active")
	})
	a.setMainSession(webSource, sessionID)
	a.setActiveSection(webSource, "main")

	web := platform.NewWeb(addr, platform.WebCallbacks{
		State: func() (platform.WebState, error) {
			return a.webState(runCtx, webSource)
		},
		Select: func(section, id string) error {
			return a.webSelect(runCtx, webSource, section, id)
		},
		SendMessage: func(text string) error {
			return a.webSendMessage(runCtx, webSource, text)
		},
		NewSession: func() error {
			return a.webNewSession(runCtx, webSource)
		},
		Action: func(name string) error {
			return a.webAction(runCtx, webSource, name)
		},
	})

	if staticDir := findWebDist(); staticDir != "" {
		web.SetStaticDir(staticDir)
	}

	if err := web.Start(runCtx); err != nil {
		return err
	}

	observeCh := make(chan bus.Msg, 256)
	a.bus.Observe(observeCh)
	go func() {
		defer a.bus.Unobserve(observeCh)
		for {
			select {
			case <-runCtx.Done():
				return
			case m := <-observeCh:
				if m.From != webSource && m.To != webSource {
					continue
				}
				web.NotifyStateChanged()
			}
		}
	}()

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		_ = web.Stop()
		return ErrClosed
	}
	if a.web != nil {
		_ = web.Stop()
		return nil
	}

	a.web = web
	a.adapters = append(a.adapters, web)
	return nil
}

func (a *App) prepareFreshSource(ctx context.Context, src string) (string, error) {
	if a.disp != nil {
		a.disp.InvalidateSource(src)
		a.disp.UnbindTeamSource(src)
	}
	a.setActiveTeam(src, "")
	a.setActiveThread(src, "")
	a.setActiveSection(src, "")

	if a.rt != nil {
		if err := a.rt.ResetSource(ctx, src); err != nil {
			return "", err
		}
	}
	if a.sm != nil {
		sess, err := a.sm.ResetSource(src)
		if err != nil {
			return "", err
		}
		return sess.ID(), nil
	}
	return "", nil
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

	tg := platform.NewTelegram(token, a.bus, a.router, platform.TelegramOptions{
		MentionMode:  a.cfg.TelegramMentionMode,
		SessionScope: a.cfg.TelegramSessionScope,
	})
	if a.reg != nil {
		names := make(map[string]string)
		for _, s := range a.reg.All() {
			names[s.Descriptor.Name] = s.Descriptor.ID
		}
		tg.SetAgentNames(names)
	}
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
	if a.hb != nil {
		a.hb.Stop()
	}
	if a.rt != nil {
		_ = a.rt.Close()
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
	cliSource := a.cliSource()

	return func() platform.StatusInfo {
		a.mu.Lock()
		model := a.cfg.ActiveModel
		a.mu.Unlock()

		u, _ := a.disp.Usage(cliSource)

		sessID, _ := a.sm.CurrentID(cliSource)

		var agents []platform.AgentInfo
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
			agents = make([]platform.AgentInfo, 0, len(states))
			for _, state := range states {
				agents = append(agents, platform.AgentInfo{
					ID:     state.Descriptor.ID,
					Name:   state.Descriptor.Name,
					Status: string(state.Status),
					Runs:   len(state.ActiveRuns),
					Caps:   state.Descriptor.Capabilities,
				})
			}
		}

		return platform.StatusInfo{
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

func (a *App) modelsInfo() command.ModelInfo {
	a.mu.Lock()
	defer a.mu.Unlock()
	return command.ModelInfo{Models: a.cfg.Models, Active: a.cfg.ActiveModel}
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

func defaultSessionDirRoot() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".mink", "sessions")
	}
	return filepath.Join(home, ".mink", "sessions")
}

func defaultSessionDir(workspace string) string {
	root := defaultSessionDirRoot()
	if strings.TrimSpace(workspace) == "" {
		return root
	}
	return filepath.Join(root, workspaceScopeID(workspace))
}

func defaultRuntimeDBPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".mink", "runtime", "runtime.db")
	}
	return filepath.Join(home, ".mink", "runtime", "runtime.db")
}

func defaultMemoryDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".mink", "memory")
	}
	return filepath.Join(home, ".mink", "memory")
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
		MaxTokens: model.MaxTokens,
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
	cfg           config.Config
	bus           *bus.Bus
	provider      llm.Provider
	selector      *llm.Sel
	sessionDir    string
	memoryDir     string
	workspace     string
	store         session.Store
	runtimeDB     *rtsqlite.DB
	memory        *memory.Store
	memoryWatcher *memory.Watcher
	hooks         *hook.Manager
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

	deps.workspace = opts.Workspace
	if deps.workspace == "" {
		deps.workspace, _ = os.Getwd()
	}

	deps.sessionDir = opts.SessionDir
	if deps.sessionDir == "" {
		deps.sessionDir = defaultSessionDir(deps.workspace)
	}
	deps.memoryDir = defaultMemoryDir()
	runtimeDB, err := rtsqlite.Open(defaultRuntimeDBPath(), rtsqlite.OpenOptions{Workspace: deps.workspace})
	if err != nil {
		if opts.SessionStore == nil {
			return runtimeDeps{}, err
		}
		fmt.Fprintf(os.Stderr, "warning: runtime sqlite disabled: %v\n", err)
	} else {
		deps.runtimeDB = runtimeDB
		deps.memory = memory.New(deps.memoryDir, deps.runtimeDB)
		deps.memoryWatcher = memory.NewWatcher(deps.memory)
	}

	deps.store = opts.SessionStore
	if deps.store == nil {
		deps.store = session.NewSQLiteStore(deps.runtimeDB, deps.workspace)
	}

	deps.hooks = opts.Hooks
	if deps.hooks == nil {
		deps.hooks = hook.NewManager()
	}

	return deps, nil
}

func workspaceScopeID(workspace string) string {
	trimmed := strings.TrimSpace(workspace)
	if trimmed == "" {
		return "default"
	}
	sum := sha256.Sum256([]byte(trimmed))
	name := filepath.Base(trimmed)
	name = strings.ToLower(strings.ReplaceAll(name, " ", "_"))
	if name == "" || name == "." || name == string(filepath.Separator) {
		name = "workspace"
	}
	return fmt.Sprintf("%s_%s", name, hex.EncodeToString(sum[:])[:8])
}

func workspacePlatformSource(kind, workspace string) string {
	_ = workspace
	return bus.Platform(kind)
}

func (a *App) cliSource() string {
	if a == nil {
		return bus.AddrPlatformCLI
	}
	return workspacePlatformSource("cli", a.workspace)
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

func buildAgentInfra(deps runtimeDeps, guard *command.GuardMux) (*session.Manager, *agent.Supervisor, *agent.Dispatcher, *agent.Registry, *mcron.Scheduler, error) {
	sm := session.NewManager(deps.store, deps.bus)
	if deps.runtimeDB != nil {
		if bindings, err := deps.runtimeDB.SessionBindings(context.Background()); err == nil {
			for source, id := range bindings {
				if _, err := sm.Get(id); err != nil {
					continue
				}
				_ = sm.RestoreSource(source, id)
			}
		}
	}
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
		RuntimeDB:  deps.runtimeDB,
		Memory:     deps.memory,
	}
	agentDeps.CronTool = tool.NewCron(config.CronPath(), cronSched)

	sup := agent.NewSupervisor(agentDeps, sm)

	var skillLoader *skill.Loader
	if deps.workspace != "" {
		skillLoader = skill.NewLoader(deps.workspace)
	}

	reg := agent.NewRegistry()
	reg.SetBus(deps.bus)
	for _, ac := range deps.cfg.Agents {
		if err := reg.Register(agent.AgentDescriptor{
			ID:            ac.ID,
			Name:          ac.Name,
			Capabilities:  ac.Capabilities,
			Model:         ac.Model,
			SoulPath:      ac.SoulPath,
			Prompt:        ac.Prompt,
			Tools:         ac.Tools,
			MaxConcurrent: ac.MaxConcurrent,
			Heartbeat:     ac.Heartbeat,
		}); err != nil {
			return nil, nil, nil, nil, nil, fmt.Errorf("register agent: %w", err)
		}
	}

	disp := agent.NewDispatcher(agentDeps, sm, skillLoader, deps.runtimeDB)
	disp.SetRegistry(reg)
	if deps.runtimeDB != nil {
		if bindings, err := deps.runtimeDB.TeamSourceBindings(context.Background()); err == nil {
			for _, binding := range bindings {
				disp.BindTeamSource(binding.Source, binding.TeamID, binding.ThreadID)
			}
		}
	}

	return sm, sup, disp, reg, cronSched, nil
}

func registerRuntimeCommands(cmdReg *command.Registry, eventBus *bus.Bus, sm *session.Manager, disp *agent.Dispatcher, sessionDir, cliSource string, mem *memory.Store, rt *rtsqlite.DB) {
	if compact := command.NewCompactCmd(eventBus); compact != nil {
		cmdReg.Register(compact)
	}
	cmdReg.Register(command.NewReplayCmd(sm, sessionDir, rt))
	cmdReg.Register(command.NewToolsCmd(func() []tool.Tool {
		if a := disp.Agent(cliSource); a != nil && a.Tools() != nil {
			return a.Tools().All()
		}
		reg := tool.NewRegistry(nil)
		return reg.All()
	}))
	cmdReg.Register(command.NewTokensCmd(disp.Usage))
	cmdReg.Register(command.NewSessionCmd(sm, disp))
	if mem != nil {
		cmdReg.Register(command.NewMemoryCmd(mem, rt))
	}
}
