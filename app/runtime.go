package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/abcdlsj/mink/agent"
	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/command"
	commandbuiltin "github.com/abcdlsj/mink/command/builtin"
	"github.com/abcdlsj/mink/config"
	mcron "github.com/abcdlsj/mink/cron"
	"github.com/abcdlsj/mink/hook"
	"github.com/abcdlsj/mink/llm"
	"github.com/abcdlsj/mink/memory"
	rtsqlite "github.com/abcdlsj/mink/runtime/store/sqlite"
	"github.com/abcdlsj/mink/session"
	"github.com/abcdlsj/mink/skill"
	"github.com/abcdlsj/mink/tool"
)

func defaultRuntimeDBPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".mink", "runtime.db")
	}
	return filepath.Join(home, ".mink", "runtime.db")
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
	memoryDir     string
	workspace     string
	store         *session.SQLiteStore
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
	deps.memoryDir = defaultMemoryDir()
	runtimeDB, err := rtsqlite.Open(defaultRuntimeDBPath(), rtsqlite.OpenOptions{Workspace: deps.workspace})
	if err != nil {
		return runtimeDeps{}, err
	}
	deps.runtimeDB = runtimeDB
	deps.memory = memory.New(deps.memoryDir, deps.runtimeDB)
	deps.memoryWatcher = memory.NewWatcher(deps.memory)

	deps.store = session.NewSQLiteStore(deps.runtimeDB, deps.workspace)

	deps.hooks = opts.Hooks
	if deps.hooks == nil {
		deps.hooks = hook.NewManager()
	}

	return deps, nil
}

func buildCommandInfra(workspace string) (*command.Registry, *command.Router, *command.GuardMux) {
	cmdReg := command.NewRegistry()
	cmdReg.Register(commandbuiltin.NewHelpCmd(cmdReg))

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
		Bus:       deps.bus,
		Provider:  deps.provider,
		Sel:       deps.selector,
		Hooks:     deps.hooks,
		ToolGuard: guard,
		Prompt:    deps.cfg.CustomPrompt,
		Config:    deps.cfg,
		RuntimeDB: deps.runtimeDB,
		Memory:    deps.memory,
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

func registerRuntimeCommands(cmdReg *command.Registry, eventBus *bus.Bus, sm *session.Manager, disp *agent.Dispatcher, cliSource string, mem *memory.Store, rt *rtsqlite.DB) {
	if compact := commandbuiltin.NewCompactCmd(eventBus); compact != nil {
		cmdReg.Register(compact)
	}
	cmdReg.Register(commandbuiltin.NewReplayCmd(sm, rt))
	cmdReg.Register(commandbuiltin.NewToolsCmd(func() []tool.Tool {
		if a := disp.Agent(cliSource); a != nil && a.Tools() != nil {
			return a.Tools().All()
		}
		reg := tool.NewRegistry(nil)
		return reg.All()
	}))
	cmdReg.Register(commandbuiltin.NewTokensCmd(disp.Usage))
	cmdReg.Register(commandbuiltin.NewSessionCmd(sm, disp))
	if mem != nil {
		cmdReg.Register(commandbuiltin.NewMemoryCmd(mem, rt))
	}
}
