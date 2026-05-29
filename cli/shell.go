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
	source := resolveCLISource(a, args)
	a.DraftSession(source)
	ui, err := newShell(ctx, a, source)
	if err != nil {
		return err
	}
	return ui.run()
}

func resolveCLISource(a *app.App, args []string) string {
	if id := flagPersona(args); id != "" {
		return "cli:agent:" + id
	}
	if id := defaultPersonaID(a); id != "" {
		return "cli:agent:" + id
	}
	return "cli"
}

func flagPersona(args []string) string {
	for i := 0; i < len(args); i++ {
		v := strings.TrimSpace(args[i])
		switch {
		case v == "--persona" || v == "-persona" || v == "-p":
			if i+1 < len(args) {
				return strings.TrimSpace(args[i+1])
			}
		case strings.HasPrefix(v, "--persona="):
			return strings.TrimSpace(strings.TrimPrefix(v, "--persona="))
		case strings.HasPrefix(v, "-persona="):
			return strings.TrimSpace(strings.TrimPrefix(v, "-persona="))
		case strings.HasPrefix(v, "-p="):
			return strings.TrimSpace(strings.TrimPrefix(v, "-p="))
		}
	}
	return ""
}

func defaultPersonaID(a *app.App) string {
	if a == nil || a.Personas() == nil {
		return ""
	}
	if id := strings.TrimSpace(a.Config().DefaultPersona); id != "" {
		if p := a.Personas().Get(id); p != nil {
			return p.ID
		}
	}
	list := a.Personas().List()
	if len(list) == 0 {
		return ""
	}
	return list[0].ID
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
	m.loadSpaceTranscript()
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
