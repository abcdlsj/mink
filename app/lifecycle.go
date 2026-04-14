package app

import (
	"context"
	"fmt"

	"github.com/abcdlsj/mink/agent"
	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/command"
	commandbuiltin "github.com/abcdlsj/mink/command/builtin"
	"github.com/abcdlsj/mink/hook"
	"github.com/abcdlsj/mink/msg"
)

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
		sources:   make(map[string]*sourceState),
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

func (a *App) Commands() *command.Registry { return a.cmdReg }
func (a *App) Hooks() *hook.Manager        { return a.hooks }
func (a *App) Usage(src string) (msg.TokenUsage, bool) {
	return a.disp.Usage(src)
}

func (a *App) Subscribe(msgType string, ch chan bus.Msg) {
	a.bus.Subscribe(msgType, ch)
}

func (a *App) Unsubscribe(msgType string, ch chan bus.Msg) {
	a.bus.Unsubscribe(msgType, ch)
}

func (a *App) Submit(src, input string) error {
	if src = trimSource(src); src == "" {
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
	if src = trimSource(src); src == "" {
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
	if src = trimSource(src); src == "" {
		return fmt.Errorf("source is required")
	}
	return a.bus.Pub(bus.Msg{
		Type:    bus.TypeSessionCompact,
		From:    src,
		To:      bus.AddrAgentMain,
		Payload: note,
	})
}
