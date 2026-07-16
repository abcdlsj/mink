package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/google/uuid"

	"github.com/abcdlsj/sumi/app"
	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/command"
	"github.com/abcdlsj/sumi/space"
)

const shellEventBatch = 128

func Run(ctx context.Context, a *app.App, args []string) error {
	launch, err := resolveCLILaunch(args)
	if err != nil {
		return err
	}
	sp, resumed, err := resolveLaunchSpace(a, launch)
	if err != nil {
		return err
	}
	source := cliSpaceSource(sp)
	sessionSource := cliSessionSource(sp)
	if resumed {
		if _, err := a.CurrentSession(sessionSource); err != nil {
			return err
		}
	} else {
		a.DraftSession(sessionSource)
	}
	ui, err := newShell(ctx, a, source)
	if err != nil {
		return err
	}
	ui.pickSpace = launch.pick
	return ui.run()
}

type cliLaunch struct {
	persona string
	resume  string
	cont    bool
	pick    bool
}

func resolveCLILaunch(args []string) (cliLaunch, error) {
	l := cliLaunch{persona: flagPersona(args)}
	for i := 0; i < len(args); i++ {
		v := strings.TrimSpace(args[i])
		switch {
		case v == "--continue" || v == "-c":
			l.cont = true
		case v == "--resume":
			if i+1 < len(args) && !strings.HasPrefix(strings.TrimSpace(args[i+1]), "-") {
				l.resume = strings.TrimSpace(args[i+1])
				i++
			} else {
				l.pick = true
			}
		case strings.HasPrefix(v, "--resume="):
			l.resume = strings.TrimSpace(strings.TrimPrefix(v, "--resume="))
			l.pick = l.resume == ""
		}
	}
	if l.cont && (l.resume != "" || l.pick) {
		return cliLaunch{}, fmt.Errorf("--continue and --resume cannot be used together")
	}
	return l, nil
}

func resolveLaunchSpace(a *app.App, launch cliLaunch) (*space.Space, bool, error) {
	if launch.resume != "" {
		sp, err := a.Spaces().LoadSpace(launch.resume)
		if err != nil {
			return nil, false, err
		}
		if !cliSpaceMatches(sp, launch.persona) {
			return nil, false, fmt.Errorf("space %s is not a matching CLI conversation", launch.resume)
		}
		return sp, true, nil
	}
	if launch.cont {
		spaces, err := listCLISpaces(a.Spaces(), launch.persona)
		if err != nil {
			return nil, false, err
		}
		if len(spaces) > 0 {
			return spaces[0], true, nil
		}
	}
	info := space.PersonaInfo{}
	kind := space.KindDirectChat
	key := newCLIChatKey(kind)
	if launch.persona != "" {
		p := a.Personas().Get(launch.persona)
		if p == nil {
			return nil, false, fmt.Errorf("persona not registered: %s", launch.persona)
		}
		kind = space.KindAgentDM
		key = newCLIChatKey(kind)
		info = space.PersonaInfo{ID: p.ID, Display: p.Display, Role: p.Description}
	}
	sp, err := a.Spaces().DraftSpace(kind, key, "", info)
	if err != nil {
		return nil, false, err
	}
	return sp, false, nil
}

func newCLIChatKey(kind space.Kind) string {
	if kind == space.KindAgentDM {
		return "cli:agent:" + uuid.NewString()
	}
	return "cli:direct:" + uuid.NewString()
}

func listCLISpaces(spaces *space.Manager, personaID string) ([]*space.Space, error) {
	all, err := spaces.ListSpaces()
	if err != nil {
		return nil, err
	}
	out := all[:0]
	for _, sp := range all {
		if cliSpaceMatches(sp, personaID) && len(sp.Messages) > 0 {
			out = append(out, sp)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out, nil
}

func cliSpaceMatches(sp *space.Space, personaID string) bool {
	if sp == nil || !strings.HasPrefix(sp.Key, "cli:") {
		return false
	}
	if personaID == "" {
		return (sp.Kind == space.KindDirectChat && strings.HasPrefix(sp.Key, "cli:direct:")) ||
			(sp.Kind == space.KindAgentDM && strings.HasPrefix(sp.Key, "cli:agent:"))
	}
	return sp.Kind == space.KindAgentDM && strings.HasPrefix(sp.Key, "cli:agent:") && space.AgentParticipantID(sp) == personaID
}

func cliSessionSource(sp *space.Space) string {
	source := cliSpaceSource(sp)
	if sp != nil && sp.Kind == space.KindAgentDM {
		return command.PersonaSessionSource(source, space.AgentParticipantID(sp))
	}
	return source
}

func cliSpaceSource(sp *space.Space) string {
	if sp == nil {
		return ""
	}
	switch sp.Kind {
	case space.KindAgentDM:
		return "cli:agent:" + sp.ID
	case space.KindChannel:
		return "cli:channel:" + sp.ID
	default:
		return "cli:direct:" + sp.ID
	}
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

type shell struct {
	ctx    context.Context
	stop   context.CancelFunc
	app    *app.App
	source string

	events <-chan bus.Event
	cancel func()

	approver  *shellApprover
	program   *tea.Program
	pickSpace bool
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
	if s.pickSpace {
		m.openChatOverlay()
	}
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
			if !shellSourceMatch("cli", ev.Source) || s.program == nil {
				continue
			}
			batch := []bus.Event{ev}
			for n := 1; n < shellEventBatch; n++ {
				select {
				case ev, ok := <-s.events:
					if !ok {
						return
					}
					if shellSourceMatch("cli", ev.Source) {
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
