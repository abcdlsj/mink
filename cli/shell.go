package cli

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/abcdlsj/sumi/app"
	"github.com/abcdlsj/sumi/bus"
)

func Run(ctx context.Context, a *app.App, args []string) error {
	if _, err := a.NewSession("cli"); err != nil {
		return err
	}
	ui, err := newShell(ctx, a, "cli")
	if err != nil {
		return err
	}
	return ui.run()
}

type shell struct {
	ctx    context.Context
	stop   context.CancelFunc
	app    *app.App
	source string

	events <-chan bus.Event
	cancel func()

	approver *shellApprover
	program  *tea.Program
}

func newShell(ctx context.Context, a *app.App, source string) (*shell, error) {
	events, cancel := a.Bus().Subscribe(256)
	runCtx, stop := context.WithCancel(ctx)
	s := &shell{
		ctx:      runCtx,
		stop:     stop,
		app:      a,
		source:   source,
		events:   events,
		cancel:   cancel,
		approver: newShellApprover(),
	}
	return s, nil
}

func (s *shell) run() error {
	defer s.cancel()
	defer s.stop()
	defer s.app.SetToolApprover(nil)

	m := newShellModel(s.ctx, s.app, s.source)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	s.program = p
	s.approver.attach(p)
	s.app.SetToolApprover(s.approver)

	go s.consume()

	_, err := p.Run()
	return err
}

func (s *shell) consume() {
	for {
		select {
		case <-s.ctx.Done():
			return
		case ev, ok := <-s.events:
			if !ok {
				return
			}
			if ev.Source != s.source || s.program == nil {
				continue
			}
			s.program.Send(shellBusMsg{Event: ev})
		}
	}
}
