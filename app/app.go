package app

import (
	"context"
	"fmt"
	"sync"

	"github.com/abcdlsj/sumi/agent"
	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/command"
	"github.com/abcdlsj/sumi/config"
	"github.com/abcdlsj/sumi/llm"
	"github.com/abcdlsj/sumi/persona"
	"github.com/abcdlsj/sumi/session"
	"github.com/abcdlsj/sumi/skill"
	"github.com/abcdlsj/sumi/space"
	"github.com/abcdlsj/sumi/store"
	"github.com/abcdlsj/sumi/task"
	"github.com/abcdlsj/sumi/tool"
)

type Plugin func(*App) error

type Entrypoint func(context.Context, *App, []string) error
type Service func(context.Context, *App) error

type App struct {
	cfg             config.Config
	bus             *bus.Bus
	store           *store.Store
	provider        llm.Provider
	sessions        *session.Manager
	spaces          *space.Manager
	tasks           *task.Manager
	spaceRouter     *space.Router
	spaceRouterOnce sync.Once
	wakeMu          sync.Mutex
	wakeQueues      map[string]chan channelWakeJob
	tools           *tool.Registry
	cmds            *command.Registry
	router          *command.Router
	skills          *skill.Loader
	personas        *persona.Registry
	runtimes        map[string]agent.RuntimeFactory
	entries         map[string]Entrypoint
	services        map[string]Service
}

func New(cfg config.Config) (*App, error) {
	cfg.Normalize()
	db, err := store.Open(cfg.DataRoot())
	if err != nil {
		return nil, err
	}
	a := &App{
		cfg:        cfg,
		bus:        bus.New(),
		store:      db,
		sessions:   session.NewManager(db),
		spaces:     space.NewManager(db, "user", "You"),
		tasks:      task.NewManager(db),
		wakeQueues: map[string]chan channelWakeJob{},
		tools:      tool.NewRegistry(cfg.Workspace, cfg.ChildEnv()),
		cmds:       command.NewRegistry(),
		runtimes:   map[string]agent.RuntimeFactory{},
		entries:    map[string]Entrypoint{},
		services:   map[string]Service{},
	}
	a.tools.SetGuard(tool.NewPolicyGuard(cfg.Workspace, cfg.PermissionsPath()))
	a.bus.OnPublish(func(ev bus.Event) {
		_ = db.AppendEvent(ev)
	})
	a.router = command.NewRouter(a.cmds)
	a.skills = skill.NewLoader(cfg.Workspace)
	a.personas = persona.NewRegistry(cfg.PersonasDir())
	if err := a.personas.Load(); err != nil {
		return nil, err
	}
	skill.RegisterTools(a.tools, a.skills)
	a.provider, err = newProvider(cfg)
	if err != nil {
		return nil, err
	}
	a.RegisterRuntime("native", agent.NewNative)
	a.registerBuiltinCommands()
	return a, nil
}

func (a *App) Close() error {
	if a == nil || a.store == nil {
		return nil
	}
	return a.store.Close()
}

func (a *App) Use(ps ...Plugin) error {
	for _, p := range ps {
		if p == nil {
			continue
		}
		if err := p(a); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) Run(ctx context.Context, args []string) error {
	for _, svc := range a.services {
		if err := svc(ctx, a); err != nil {
			return err
		}
	}
	name := "cli"
	if len(args) > 0 {
		if _, ok := a.entries[args[0]]; ok {
			name = args[0]
			args = args[1:]
		}
	}
	entry := a.entries[name]
	if entry == nil {
		return fmt.Errorf("entrypoint not found: %s", name)
	}
	return entry(ctx, a, args)
}

func (a *App) RegisterTool(t tool.Tool) {
	a.tools.Register(t)
}

func (a *App) RegisterCommand(c command.Command) {
	a.cmds.Register(c)
}

func (a *App) RegisterRuntime(name string, f agent.RuntimeFactory) {
	a.runtimes[name] = f
}

func (a *App) HasRuntime(name string) bool {
	return a.runtimes[name] != nil
}

func (a *App) RegisterEntrypoint(name string, f Entrypoint) {
	a.entries[name] = f
}

func (a *App) RegisterService(name string, f Service) {
	a.services[name] = f
}

func (a *App) Bus() *bus.Bus {
	return a.bus
}

func (a *App) Config() config.Config {
	return a.cfg
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
