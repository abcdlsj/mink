package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/abcdlsj/mink/app"
	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/command"
	"github.com/abcdlsj/mink/textutil"
	"github.com/abcdlsj/mink/tool"
)

type shellFocus int

const (
	focusTranscript shellFocus = iota
	focusComposer
)

type shellOverlay int

const (
	overlayNone shellOverlay = iota
	overlayApproval
)

type shellBusMsg struct {
	Event bus.Event
}

type shellTurnDoneMsg struct {
	Reply string
	Err   error
}

type shellApprovalEnqueuedMsg struct {
	Request *approvalRequest
}

type shellApprovalDroppedMsg struct {
	ID int
}

type shellTickMsg struct{}

type shellTurn struct {
	assistantIndex int
	streamed       bool
	commandHandled bool
	errored        bool
	toolCount      int
}

type shellToolRef struct {
	Item int
	Seg  int
}

type shellSpan struct {
	Start int
	End   int
}

type shellModel struct {
	ctx    context.Context
	app    *app.App
	source string

	width  int
	height int

	viewport viewport.Model
	input    textarea.Model

	items     []*chatItem
	spans     []shellSpan
	toolItems map[string]shellToolRef
	approvals []*approvalRequest
	turn      shellTurn

	focus    shellFocus
	overlay  shellOverlay
	expanded int
	selected int
	spinner  int
	busy     bool
	follow   bool
}

func newShellModel(ctx context.Context, a *app.App, source string) shellModel {
	in := textarea.New()
	in.Prompt = ""
	in.Placeholder = "Ask Mink anything. Use !command for local commands."
	in.ShowLineNumbers = false
	in.SetHeight(4)
	in.KeyMap.InsertNewline = key.NewBinding(
		key.WithKeys("ctrl+j"),
		key.WithHelp("ctrl+j", "newline"),
	)
	in.FocusedStyle.Base = in.FocusedStyle.Base.BorderStyle(shellTheme.NoBorder)
	in.BlurredStyle.Base = in.BlurredStyle.Base.BorderStyle(shellTheme.NoBorder)
	in.FocusedStyle.Text = shellTheme.Text
	in.BlurredStyle.Text = shellTheme.TextMuted
	in.FocusedStyle.Placeholder = shellTheme.TextMuted
	in.BlurredStyle.Placeholder = shellTheme.TextMuted
	in.FocusedStyle.CursorLine = shellTheme.Text
	in.BlurredStyle.CursorLine = shellTheme.TextMuted

	vp := viewport.New(0, 0)
	vp.KeyMap = viewport.KeyMap{}
	vp.MouseWheelEnabled = false

	return shellModel{
		ctx:       ctx,
		app:       a,
		source:    source,
		viewport:  vp,
		input:     in,
		toolItems: map[string]shellToolRef{},
		turn:      shellTurn{assistantIndex: -1},
		focus:     focusComposer,
		expanded:  -1,
		selected:  -1,
		follow:    true,
	}
}

func (m shellModel) Init() tea.Cmd {
	return tea.Batch(m.input.Focus(), shellTick())
}

func (m shellModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.syncLayout()
		return m, nil
	case shellTickMsg:
		m.spinner++
		if m.shouldTick() {
			return m, shellTick()
		}
		return m, nil
	case shellBusMsg:
		m.handleEvent(msg.Event)
		return m, m.nextTick()
	case shellTurnDoneMsg:
		m.finishTurn(msg.Reply, msg.Err)
		return m, m.nextTick()
	case shellApprovalEnqueuedMsg:
		m.approvals = append(m.approvals, msg.Request)
		m.overlay = overlayApproval
		return m, m.nextTick()
	case shellApprovalDroppedMsg:
		m.dropApproval(msg.ID)
		return m, m.nextTick()
	case tea.KeyMsg:
		return m.updateKey(msg)
	}

	var cmd tea.Cmd
	if m.focus == focusComposer && m.overlay == overlayNone {
		m.input, cmd = m.input.Update(msg)
		m.syncLayout()
		return m, cmd
	}
	return m, nil
}

func (m shellModel) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	base := shellTheme.Base.Width(m.width).Height(m.height).Render(
		lipJoinVertical(
			m.renderHeader(),
			m.renderTranscript(),
			m.renderComposer(),
			m.renderFooter(),
		),
	)
	switch m.overlay {
	case overlayApproval:
		return m.renderOverlay(base, "Approval", m.approvalBody())
	default:
		return base
	}
}

func (m *shellModel) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}
	if m.overlay == overlayApproval {
		m.handleApprovalKey(msg.String())
		return *m, m.nextTick()
	}

	switch msg.String() {
	case "tab":
		return m.toggleFocus()
	case "ctrl+o":
		if len(m.items) > 0 {
			m.toggleExpanded(m.selected)
		}
		return *m, nil
	}

	if m.focus == focusComposer {
		switch msg.String() {
		case "enter":
			return m.submit()
		case "esc":
			m.focus = focusTranscript
			m.input.Blur()
			return *m, nil
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		m.syncLayout()
		return *m, cmd
	}

	switch msg.String() {
	case "j", "down":
		m.moveSelection(1)
	case "k", "up":
		m.moveSelection(-1)
	case "g", "home":
		m.selectItem(0)
	case "G", "end":
		m.selectItem(len(m.items) - 1)
	case "pgdown":
		m.pageSelection(1)
	case "pgup":
		m.pageSelection(-1)
	case "enter":
		if len(m.items) > 0 {
			m.toggleExpanded(m.selected)
		}
	case "esc":
		m.focus = focusComposer
		return *m, m.input.Focus()
	}
	return *m, nil
}

func (m *shellModel) submit() (tea.Model, tea.Cmd) {
	if m.busy {
		m.addItem(chatItem{
			Kind: itemNotice,
			Time: time.Now(),
			Segments: []chatSegment{{
				Kind: segText,
				Text: "The current turn is still running. Wait for it to finish before sending another input.",
				Time: time.Now(),
			}},
		})
		return *m, nil
	}
	text := textutil.Valid(strings.TrimSpace(m.input.Value()))
	if text == "" {
		return *m, nil
	}
	m.input.Reset()
	m.input.SetHeight(4)
	m.busy = true
	m.toolItems = map[string]shellToolRef{}
	m.turn = shellTurn{
		assistantIndex: -1,
		commandHandled: command.IsCommand(text),
	}
	m.addItem(chatItem{
		Kind: itemUser,
		Time: time.Now(),
		Segments: []chatSegment{{
			Kind: segText,
			Text: text,
			Time: time.Now(),
		}},
	})
	return *m, func() tea.Msg {
		reply, err := m.app.HandleInput(m.ctx, m.source, text)
		return shellTurnDoneMsg{Reply: reply, Err: err}
	}
}

func (m *shellModel) handleEvent(ev bus.Event) {
	switch ev.Type {
	case bus.TurnStarted:
		m.busy = true
	case bus.TurnChunk:
		m.turn.streamed = true
		m.appendAssistant(ev.Text)
	case bus.ToolCallStarted:
		m.turn.toolCount++
		m.startTool(ev)
	case bus.ToolCallFinished:
		m.finishTool(ev, false)
	case bus.ToolCallFailed:
		m.finishTool(ev, true)
	case bus.CommandHandled:
		m.turn.commandHandled = true
		if strings.TrimSpace(ev.Err) != "" {
			m.turn.errored = true
			m.addItem(chatItem{
				Kind: itemError,
				Time: eventTime(ev),
				Segments: []chatSegment{{
					Kind: segText,
					Text: ev.Err,
					Time: eventTime(ev),
				}},
			})
			return
		}
		if m.turn.toolCount == 0 && strings.TrimSpace(ev.Text) != "" {
			m.appendAssistantAt(eventTime(ev), ev.Text)
		}
	case bus.ServiceNotice:
		m.addItem(chatItem{
			Kind: itemNotice,
			Time: eventTime(ev),
			Segments: []chatSegment{{
				Kind: segText,
				Text: ev.Text,
				Time: eventTime(ev),
			}},
		})
	case bus.ModelChanged:
		m.addItem(chatItem{
			Kind: itemNotice,
			Time: eventTime(ev),
			Segments: []chatSegment{{
				Kind: segText,
				Text: "Model switched to " + ev.Text,
				Time: eventTime(ev),
			}},
		})
	case bus.TurnFinished:
		m.busy = false
	case bus.TurnError:
		m.turn.errored = true
		m.busy = false
		m.addItem(chatItem{
			Kind: itemError,
			Time: eventTime(ev),
			Segments: []chatSegment{{
				Kind: segText,
				Text: ev.Err,
				Time: eventTime(ev),
			}},
		})
	}
}

func (m *shellModel) finishTurn(reply string, err error) {
	m.busy = false
	if err != nil {
		if !m.turn.errored {
			m.addItem(chatItem{
				Kind: itemError,
				Time: time.Now(),
				Segments: []chatSegment{{
					Kind: segText,
					Text: err.Error(),
					Time: time.Now(),
				}},
			})
		}
		m.turn = shellTurn{assistantIndex: -1}
		return
	}
	reply = textutil.Valid(strings.TrimSpace(reply))
	if !m.turn.commandHandled && !m.turn.streamed && reply != "" {
		m.appendAssistant(reply)
	}
	m.turn = shellTurn{assistantIndex: -1}
}

func (m *shellModel) addItem(item chatItem) int {
	if item.Time.IsZero() {
		item.Time = time.Now()
	}
	follow := m.follow || m.selected >= len(m.items)-1
	m.items = append(m.items, &item)
	if follow {
		m.selected = len(m.items) - 1
		m.follow = true
	}
	m.syncViewport()
	return len(m.items) - 1
}

func (m *shellModel) appendAssistant(text string) {
	m.appendAssistantAt(time.Now(), text)
}

func (m *shellModel) appendAssistantAt(t time.Time, text string) {
	text = textutil.Valid(text)
	if text == "" {
		return
	}
	if m.turn.assistantIndex < 0 {
		m.turn.assistantIndex = m.addItem(chatItem{
			Kind: itemAssistant,
			Time: t,
		})
	}
	item := m.items[m.turn.assistantIndex]
	item.appendText(text)
	m.syncViewport()
}

func (m *shellModel) startTool(ev bus.Event) {
	if m.turn.assistantIndex < 0 {
		m.turn.assistantIndex = m.addItem(chatItem{
			Kind: itemAssistant,
			Time: eventTime(ev),
		})
	}
	item := m.items[m.turn.assistantIndex]
	seg := item.addTool(ev.Tool, summarizeToolAction(ev.Tool, ev.Input), renderToolDetail(ev, false), eventTime(ev))
	idx := m.turn.assistantIndex
	if ev.ToolCallID != "" {
		m.toolItems[ev.ToolCallID] = shellToolRef{Item: idx, Seg: seg}
	}
	m.syncViewport()
}

func (m *shellModel) finishTool(ev bus.Event, failed bool) {
	ref, ok := m.toolItems[ev.ToolCallID]
	if !ok {
		m.startTool(ev)
		ref = m.toolItems[ev.ToolCallID]
	}
	item := m.items[ref.Item]
	if ref.Seg < 0 || ref.Seg >= len(item.Segments) {
		return
	}
	seg := &item.Segments[ref.Seg]
	if failed {
		seg.Status = "failed"
		if strings.TrimSpace(ev.Err) != "" {
			seg.Text += " -> " + textutil.Preview(ev.Err, 72)
		}
	} else {
		seg.Status = "done"
		if out := summarizeToolOutput(ev.Output); out != "" {
			seg.Text += " -> " + out
		}
	}
	seg.Detail = renderToolDetail(ev, failed)
	m.syncViewport()
}

func (m *shellModel) moveSelection(delta int) {
	if len(m.items) == 0 {
		return
	}
	m.selectItem(m.selected + delta)
}

func (m *shellModel) pageSelection(delta int) {
	if len(m.items) == 0 || m.viewport.Height <= 0 {
		return
	}
	step := max(1, m.viewport.Height/4)
	target := m.selected
	if target < 0 {
		target = len(m.items) - 1
	}
	for moved := 0; moved < step; moved++ {
		target += delta
		if target < 0 || target >= len(m.items) {
			break
		}
	}
	m.selectItem(target)
}

func (m *shellModel) selectItem(i int) {
	if len(m.items) == 0 {
		m.selected = -1
		return
	}
	if i < 0 {
		i = 0
	}
	if i >= len(m.items) {
		i = len(m.items) - 1
	}
	m.selected = i
	m.follow = i == len(m.items)-1
	m.syncViewport()
}

func (m *shellModel) toggleExpanded(i int) {
	if i < 0 || i >= len(m.items) {
		return
	}
	if m.expanded == i {
		m.expanded = -1
	} else {
		m.expanded = i
	}
	m.keepSelectionVisible()
	m.syncViewport()
}

func (m *shellModel) toggleFocus() (tea.Model, tea.Cmd) {
	if m.focus == focusComposer {
		m.focus = focusTranscript
		m.input.Blur()
		return *m, nil
	}
	m.focus = focusComposer
	return *m, m.input.Focus()
}

func (m *shellModel) dropApproval(id int) {
	out := m.approvals[:0]
	for _, req := range m.approvals {
		if req.id != id {
			out = append(out, req)
		}
	}
	m.approvals = out
	if len(m.approvals) == 0 && m.overlay == overlayApproval {
		m.overlay = overlayNone
	}
}

func (m *shellModel) handleApprovalKey(key string) {
	if len(m.approvals) == 0 {
		m.overlay = overlayNone
		return
	}
	req := m.approvals[0]
	switch key {
	case "a":
		req.resp <- tool.AllowAlways
	case "y", "enter":
		req.resp <- tool.AllowOnce
	case "n", "esc":
		req.resp <- tool.Denied
	default:
		return
	}
	m.dropApproval(req.id)
}

func (m *shellModel) syncLayout() {
	if m.width <= 0 || m.height <= 0 {
		return
	}
	inWidth := max(20, m.width-4)
	inHeight := clamp(len(strings.Split(textutil.Valid(m.input.Value()), "\n"))+1, 4, 8)
	m.input.SetWidth(inWidth)
	m.input.SetHeight(inHeight)
	if m.focus == focusComposer {
		_ = m.input.Focus()
	} else {
		m.input.Blur()
	}
	header := 3
	footer := 1
	composer := inHeight + 4
	m.viewport.Width = max(20, m.width-4)
	m.viewport.Height = max(3, m.height-header-footer-composer)
	m.syncViewport()
}

func (m *shellModel) syncViewport() {
	if m.viewport.Width <= 0 {
		return
	}
	if len(m.items) == 0 {
		m.spans = nil
		m.viewport.SetContent("")
		return
	}
	lines := make([]string, 0, len(m.items)*4)
	spans := make([]shellSpan, len(m.items))
	for i, item := range m.items {
		start := len(lines)
		lines = append(lines, m.renderItem(item, i)...)
		if i < len(m.items)-1 {
			lines = append(lines, "")
		}
		spans[i] = shellSpan{Start: start, End: len(lines)}
	}
	m.spans = spans
	m.viewport.SetContent(strings.Join(lines, "\n"))
	if m.follow {
		m.viewport.GotoBottom()
		return
	}
	m.keepSelectionVisible()
}

func (m *shellModel) keepSelectionVisible() {
	if m.selected < 0 || m.selected >= len(m.spans) || m.viewport.Height <= 0 {
		return
	}
	span := m.spans[m.selected]
	top := m.viewport.YOffset
	bot := top + m.viewport.Height
	if span.Start < top {
		m.viewport.SetYOffset(span.Start)
		return
	}
	if span.End > bot {
		m.viewport.SetYOffset(span.End - m.viewport.Height)
	}
}

func (m *shellModel) shouldTick() bool {
	return m.busy || len(m.approvals) > 0
}

func (m *shellModel) nextTick() tea.Cmd {
	if m.shouldTick() {
		return shellTick()
	}
	return nil
}

func shellTick() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg {
		return shellTickMsg{}
	})
}

type cliState struct {
	Runtime string
	Model   string
	Cwd     string
	Session string
}

func (m *shellModel) state() cliState {
	rt := strings.TrimSpace(m.app.Config().Runtime)
	if rt == "" {
		rt = "native"
	}
	model := m.app.CurrentModel()
	switch rt {
	case "claude":
		model = "claude native"
	case "codex":
		model = "codex native"
	}
	ws := strings.TrimSpace(m.app.Workspace())
	if ws == "" {
		ws = "."
	}
	sid := "(new)"
	if s, err := m.app.CurrentSession(m.source); err == nil && s != nil && s.ID != "" {
		sid = s.ID
	}
	return cliState{
		Runtime: rt,
		Model:   model,
		Cwd:     shortPath(filepath.Clean(ws)),
		Session: sid,
	}
}

func shortPath(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == home {
		return "~"
	}
	if strings.HasPrefix(path, home+"/") {
		return "~/" + strings.TrimPrefix(path, home+"/")
	}
	return path
}

func eventTime(ev bus.Event) time.Time {
	if ev.Time.IsZero() {
		return time.Now()
	}
	return ev.Time
}
