package mink

import (
	"os"
	"path/filepath"

	"github.com/abcdlsj/mink/agent"
	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/command"
	"github.com/abcdlsj/mink/config"
	mcron "github.com/abcdlsj/mink/cron"
	"github.com/abcdlsj/mink/hook"
	"github.com/abcdlsj/mink/llm"
	"github.com/abcdlsj/mink/session"
	"github.com/abcdlsj/mink/skill"
	"github.com/abcdlsj/mink/tool"
)

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
	disp.SetSelfUpdateTool(tool.NewSelfUpdate(sm, disp))

	return sm, sup, disp, cronSched
}

func registerRuntimeCommands(cmdReg *command.Registry, eventBus *bus.Bus, sm *session.Manager, disp *agent.Dispatcher) {
	if compact := command.NewCompactCmd(eventBus); compact != nil {
		cmdReg.Register(compact)
	}
	cmdReg.Register(command.NewTokensCmd(disp.Usage))
	cmdReg.Register(command.NewSessionCmd(sm, disp))
}
