package cli

import (
	"context"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/abcdlsj/sumi/app"
	"github.com/abcdlsj/sumi/bus"
)

const shellEventBatch = 128

func Run(ctx context.Context, a *app.App, args []string) error {
	a.DraftSession("cli")
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
	events, cancel := a.Bus().Subscribe(4096)
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
			if !shellSourceMatch(s.source, ev.Source) || s.program == nil {
				continue
			}
			batch := []bus.Event{ev}
			for n := 1; n < shellEventBatch; n++ {
				select {
				case ev, ok := <-s.events:
					if !ok {
						return
					}
					if shellSourceMatch(s.source, ev.Source) {
						batch = appendEvent(batch, ev)
					}
				default:
					s.program.Send(shellBusMsg{Events: batch})
					batch = nil
				}
				if batch == nil {
					break
				}
			}
			if len(batch) > 0 {
				s.program.Send(shellBusMsg{Events: batch})
			}
		}
	}
}

func shellSourceMatch(base, source string) bool {
	base = strings.TrimSpace(base)
	source = strings.TrimSpace(source)
	return source == base || strings.HasPrefix(source, base+":")
}

func appendEvent(evs []bus.Event, ev bus.Event) []bus.Event {
	if len(evs) == 0 || !mergeStreamEvent(&evs[len(evs)-1], ev) {
		return append(evs, ev)
	}
	return evs
}

func mergeStreamEvent(dst *bus.Event, ev bus.Event) bool {
	if dst.Source != ev.Source || dst.SessionID != ev.SessionID || dst.Type != ev.Type {
		return false
	}
	switch ev.Type {
	case bus.TurnChunk, bus.TurnReasoning:
		dst.Text += ev.Text
		return true
	default:
		return false
	}
}
