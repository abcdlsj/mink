package app

import (
	"context"
	"encoding/json"
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
	"github.com/google/uuid"
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
	workerOwnerID   string
	worker          *deliveryWorker
	startOnce       sync.Once
	startErr        error
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
		workerOwnerID: "worker-" + uuid.NewString()[:8],
		tools:      tool.NewRegistry(cfg.Workspace, cfg.ChildEnv()),
		cmds:       command.NewRegistry(),
		runtimes:   map[string]agent.RuntimeFactory{},
		entries:    map[string]Entrypoint{},
		services:   map[string]Service{},
	}
	guard := tool.NewPolicyGuard(cfg.Workspace, cfg.PermissionsPath())
	guard.SetAudit(a.auditAction)
	a.tools.SetGuard(guard)
	a.bus.OnPublish(func(ev bus.Event) {
		_ = db.AppendEvent(ev)
	})
	a.spaces.SetEventSink(a.bus.Publish)
	a.tasks.SetEventSink(a.bus.Publish)
	a.router = command.NewRouter(a.cmds)
	a.skills = skill.NewLoader(cfg.Workspace)
	a.personas = persona.NewRegistry(cfg.PersonasDir())
	if err := a.personas.Load(); err != nil {
		return nil, err
	}
	skill.RegisterTools(a.tools, a.skills, a.auditSkill)
	a.registerTaskTools()
	a.provider, err = newProvider(cfg)
	if err != nil {
		return nil, err
	}
	a.RegisterRuntime("native", agent.NewNative)
	if err := a.registerBuiltinCommands(); err != nil {
		return nil, err
	}
	return a, nil
}

func (a *App) auditSkill(action, name string) {
	if a == nil || a.bus == nil {
		return
	}
	typ := bus.SkillUsed
	switch action {
	case "listed":
		typ = bus.SkillListed
	case "described":
		typ = bus.SkillDescribed
	}
	a.bus.Publish(bus.Event{Type: typ, Tool: "skill", Text: name})
}

func (a *App) auditAction(ctx context.Context, req tool.Request, approval tool.Approval) {
	if a == nil || a.bus == nil {
		return
	}
	data, _ := json.Marshal(req.Proposal)
	a.bus.Publish(bus.Event{
		Type:   bus.ActionProposal,
		Source: command.SourceFrom(ctx),
		Tool:   req.Tool,
		Input:  string(data),
		Output: approvalLabel(approval),
	})
}

func approvalLabel(v tool.Approval) string {
	switch v {
	case tool.AllowOnce:
		return "allow_once"
	case tool.AllowAlways:
		return "allow_always"
	default:
		return "denied"
	}
}

func (a *App) Close() error {
	if a == nil {
		return nil
	}
	if a.worker != nil {
		a.worker.stop()
	}
	if a.store == nil {
		return nil
	}
	return a.store.Close()
}

// Start performs one-time startup: reconcile persisted routing intents into
// durable deliveries (recovering any "fact written, delivery not yet created"
// gap) and then launch the single delivery worker. It is idempotent — the CLI
// entrypoint, the desktop Wails bridge, and any other host all funnel through
// here, so calling it more than once is a no-op after the first success.
//
// If reconcile fails, the worker is NOT started and the error is returned: the
// process must not begin accepting routed traffic on top of an inconsistent
// delivery set. A cached error is returned on every later call so no host can
// silently proceed past a failed start.
func (a *App) Start(ctx context.Context) error {
	if a == nil {
		return nil
	}
	a.startOnce.Do(func() {
		if err := a.reconcileDeliveries(ctx); err != nil {
			a.startErr = fmt.Errorf("reconcile deliveries: %w", err)
			return
		}
		a.worker = newDeliveryWorker(a, a.workerOwnerID)
		a.worker.start(ctx)
	})
	return a.startErr
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
	// Start the durable delivery subsystem (reconcile + worker) before any
	// service or entrypoint begins accepting routed traffic. Start is idempotent
	// via startOnce, so hosts that also call it explicitly are unaffected.
	if err := a.Start(ctx); err != nil {
		return err
	}
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

func (a *App) RegisterCommand(c command.Command) error {
	return a.cmds.Register(c)
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
