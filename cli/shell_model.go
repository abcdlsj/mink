package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/session"
	"github.com/abcdlsj/sumi/textutil"
	"github.com/abcdlsj/sumi/tool"
)

type shellFocus int

const (
	focusTranscript shellFocus = iota
	focusComposer
)

type shellOverlay int

const (
	overlayNone shellOverlay = iota
	overlaySession
)

const shellHeaderHeight = 2

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

type shellStatusLineMsg struct {
	Text string
}

type shellTurn struct {
	assistantIndex int
	started        time.Time
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
	app    shellApp
	source string

	width  int
	height int

	viewport viewport.Model
	input    textarea.Model

	items        []*chatItem
	spans        []shellSpan
	toolItems    map[string]shellToolRef
	approvals    []*approvalRequest
	approvalPick int
	queue        []string
	suggests     []completionHint
	suggestRows  int
	files        []string
	filesOK      bool
	filesLoading bool
	pendingCmd   tea.Cmd
	sessions     []*session.Session
	statusLine   string
	turn         shellTurn

	focus      shellFocus
	overlay    shellOverlay
	expanded   int
	selected   int
	suggest    int
	session    int
	spinner    int
	busy       bool
	statusBusy bool
	statusAt   time.Time
	follow     bool
}

func newShellModel(ctx context.Context, a shellApp, source string) shellModel {
	in := textarea.New()
	in.Prompt = ""
	in.SetPromptFunc(2, func(line int) string {
		if line == 0 {
			return "› "
		}
		return "  "
	})
	in.Placeholder = "Ask Sumi to do anything"
	in.ShowLineNumbers = false
	in.SetHeight(2)
	in.KeyMap.InsertNewline = key.NewBinding(
		key.WithKeys("ctrl+j"),
		key.WithHelp("ctrl+j", "newline"),
	)
	in.FocusedStyle.Base = in.FocusedStyle.Base.BorderStyle(shellTheme.NoBorder)
	in.BlurredStyle.Base = in.BlurredStyle.Base.BorderStyle(shellTheme.NoBorder)
	in.FocusedStyle.Text = shellTheme.Text
	in.BlurredStyle.Text = shellTheme.TextMuted
	in.FocusedStyle.Prompt = shellTheme.Prompt
	in.BlurredStyle.Prompt = shellTheme.TextMuted
	in.FocusedStyle.Placeholder = shellTheme.TextMuted
	in.BlurredStyle.Placeholder = shellTheme.TextMuted
	in.FocusedStyle.CursorLine = shellTheme.Text
	in.BlurredStyle.CursorLine = shellTheme.TextMuted
	in.FocusedStyle.CursorLineNumber = shellTheme.TextMuted
	in.BlurredStyle.CursorLineNumber = shellTheme.TextMuted
	in.FocusedStyle.EndOfBuffer = shellTheme.TextMuted
	in.BlurredStyle.EndOfBuffer = shellTheme.TextMuted
	in.FocusedStyle.LineNumber = shellTheme.TextMuted
	in.BlurredStyle.LineNumber = shellTheme.TextMuted

	vp := viewport.New(0, 0)
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
	root := ""
	if m.app != nil {
		root = m.app.Workspace()
	}
	return tea.Batch(m.input.Focus(), shellTick(), m.statusLineCmd(true), loadWorkspaceFilesCmd(root))
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
		var cmds []tea.Cmd
		if m.shouldTick() {
			cmds = append(cmds, shellTick())
		}
		if cmd := m.statusLineCmd(false); cmd != nil {
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)
	case shellStatusLineMsg:
		m.statusLine = msg.Text
		m.statusBusy = false
		m.statusAt = time.Now()
		return m, nil
	case shellFilesLoadedMsg:
		if m.app != nil && msg.Root == m.app.Workspace() {
			m.files = msg.Files
			m.filesOK = true
			m.filesLoading = false
			m.refreshSuggestions()
		}
		return m, nil
	case shellBusMsg:
		m.handleEvent(msg.Event)
		return m, m.nextTick()
	case shellTurnDoneMsg:
		if cmd := m.finishTurn(msg.Reply, msg.Err); cmd != nil {
			return m, tea.Batch(cmd, m.nextTick())
		}
		return m, m.nextTick()
	case shellApprovalEnqueuedMsg:
		m.approvals = append(m.approvals, msg.Request)
		return m, m.nextTick()
	case shellApprovalDroppedMsg:
		m.dropApproval(msg.ID)
		return m, m.nextTick()
	case tea.KeyMsg:
		return m.updateKey(msg)
	case tea.MouseMsg:
		if m.overlay == overlayNone {
			return m.updateMouse(msg)
		}
	}

	var cmd tea.Cmd
	if m.focus == focusComposer && m.overlay == overlayNone {
		m.input, cmd = m.input.Update(msg)
		m.cleanInput()
		m.syncLayout()
		if m.pendingCmd != nil {
			cmd = tea.Batch(cmd, m.pendingCmd)
			m.pendingCmd = nil
		}
		return m, cmd
	}
	if m.focus == focusTranscript && m.overlay == overlayNone {
		m.viewport, cmd = m.viewport.Update(msg)
		m.follow = false
		return m, cmd
	}
	return m, nil
}

func (m shellModel) updateMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if !tea.MouseEvent(msg).IsWheel() {
		return m, nil
	}
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	m.follow = false
	return m, cmd
}

func (m shellModel) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	st := m.state()
	base := shellTheme.Base.Width(m.width).Height(m.height).Render(
		lipJoinVertical(
			m.renderHeader(st),
			m.renderTranscript(),
			m.renderStatus(),
			m.renderComposer(),
			m.renderFooter(st),
		),
	)
	switch m.overlay {
	case overlaySession:
		return m.renderOverlay(base, "Sessions", m.sessionBody())
	default:
		return base
	}
}

func (m *shellModel) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}
	if m.overlay == overlaySession {
		m.handleSessionKey(msg.String())
		return *m, nil
	}
	if len(m.approvals) > 0 {
		if m.handleApprovalKey(msg.String()) {
			return *m, m.nextTick()
		}
		return *m, nil
	}

	if m.focus == focusComposer && m.overlay == overlayNone && len(m.suggests) > 0 {
		switch msg.String() {
		case "enter":
			if m.exactSuggestion() {
				return m.submit()
			}
			m.acceptSuggestion()
			m.syncLayout()
			return *m, nil
		case "tab":
			m.acceptSuggestion()
			m.syncLayout()
			return *m, nil
		case "down", "ctrl+n":
			m.moveSuggestion(1)
			return *m, nil
		case "up", "ctrl+p", "shift+tab":
			m.moveSuggestion(-1)
			return *m, nil
		case "esc":
			m.suggests = nil
			return *m, nil
		}
	}

	switch msg.String() {
	case "ctrl+r":
		m.openSessionOverlay()
		return *m, nil
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
		m.cleanInput()
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
	m.cleanInput()
	text := textutil.Valid(strings.TrimSpace(cleanTerminalInput(m.input.Value())))
	if text == "" {
		return *m, nil
	}
	if isSessionSelectorCommand(text) {
		m.input.Reset()
		m.input.SetHeight(2)
		m.clearSuggestions()
		m.openSessionOverlay()
		return *m, nil
	}
	m.input.Reset()
	m.input.SetHeight(2)
	m.suggests = nil
	if m.busy {
		m.queue = append(m.queue, text)
		m.addTextItem(itemNotice, fmt.Sprintf("Queued #%d: %s", len(m.queue), textutil.Preview(text, 96)), time.Now())
		m.syncLayout()
		return *m, nil
	}
	return *m, m.startInput(text)
}

func (m *shellModel) startInput(text string) tea.Cmd {
	m.busy = true
	m.toolItems = map[string]shellToolRef{}
	m.turn = shellTurn{
		assistantIndex: -1,
		started:        time.Now(),
	}
	m.addTextItem(itemUser, text, time.Now())
	m.syncLayout()
	return func() tea.Msg {
		reply, err := m.app.HandleInput(m.ctx, m.source, text)
		return shellTurnDoneMsg{Reply: reply, Err: err}
	}
}

func (m *shellModel) handleEvent(ev bus.Event) {
	switch ev.Type {
	case bus.TurnStarted:
		m.busy = true
		if m.turn.started.IsZero() {
			m.turn.started = eventTime(ev)
		}
	case bus.TurnChunk:
		m.turn.streamed = true
		m.appendAssistant(ev.Text)
	case bus.TurnReasoning:
		m.appendReasoning(ev.Text)
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
			m.addTextItem(itemError, ev.Err, eventTime(ev))
			return
		}
		if m.turn.toolCount == 0 && strings.TrimSpace(ev.Text) != "" {
			m.appendAssistantAt(eventTime(ev), ev.Text)
		}
	case bus.ServiceNotice:
		m.addTextItem(itemNotice, ev.Text, eventTime(ev))
	case bus.ModelChanged:
		m.addTextItem(itemNotice, "Model switched to "+ev.Text, eventTime(ev))
	case bus.TurnFinished:
		m.busy = false
	case bus.TurnError:
		m.turn.errored = true
		m.busy = false
		m.addTextItem(itemError, ev.Err, eventTime(ev))
	}
}

func (m *shellModel) finishTurn(reply string, err error) tea.Cmd {
	m.busy = false
	if err != nil {
		if !m.turn.errored {
			m.addTextItem(itemError, err.Error(), time.Now())
		}
		m.turn = shellTurn{assistantIndex: -1}
		return m.startQueued()
	}
	reply = textutil.Valid(strings.TrimSpace(reply))
	if !m.turn.commandHandled && !m.turn.streamed && reply != "" {
		m.appendAssistant(reply)
	}
	m.turn = shellTurn{assistantIndex: -1}
	return m.startQueued()
}

func (m *shellModel) startQueued() tea.Cmd {
	for len(m.queue) > 0 {
		text := m.queue[0]
		m.queue = m.queue[1:]
		text = textutil.Valid(strings.TrimSpace(cleanTerminalInput(text)))
		if text == "" {
			continue
		}
		return m.startInput(text)
	}
	return nil
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

func (m *shellModel) addTextItem(kind int, text string, t time.Time) int {
	if t.IsZero() {
		t = time.Now()
	}
	return m.addItem(chatItem{
		Kind: kind,
		Time: t,
		Segments: []chatSegment{{
			Kind: segText,
			Text: text,
			Time: t,
		}},
	})
}

func (m *shellModel) assistantItem(t time.Time) *chatItem {
	if t.IsZero() {
		t = time.Now()
	}
	if m.turn.assistantIndex < 0 {
		m.turn.assistantIndex = m.addItem(chatItem{
			Kind: itemAssistant,
			Time: t,
		})
	}
	return m.items[m.turn.assistantIndex]
}

func (m *shellModel) appendAssistant(text string) {
	m.appendAssistantAt(time.Now(), text)
}

func (m *shellModel) appendAssistantAt(t time.Time, text string) {
	text = textutil.Valid(text)
	if text == "" {
		return
	}
	if m.turn.assistantIndex < 0 && m.mergeLateAssistant(text) {
		m.syncViewport()
		return
	}
	item := m.assistantItem(t)
	if text = assistantDelta(item, text); text == "" {
		m.syncViewport()
		return
	}
	item.appendText(text)
	m.syncViewport()
}

func (m *shellModel) mergeLateAssistant(text string) bool {
	if len(m.items) == 0 {
		return false
	}
	item := m.items[len(m.items)-1]
	if item == nil || item.Kind != itemAssistant {
		return false
	}
	cur := assistantText(item)
	switch {
	case cur == "", text == "":
		return false
	case strings.Contains(cur, text), strings.HasPrefix(cur, text):
		return true
	case strings.HasPrefix(text, cur):
		item.appendText(text[len(cur):])
		return true
	default:
		return false
	}
}

func assistantDelta(item *chatItem, text string) string {
	cur := assistantText(item)
	switch {
	case cur == "", text == "":
		return text
	case strings.HasPrefix(cur, text):
		return ""
	case strings.HasPrefix(text, cur):
		return text[len(cur):]
	default:
		return text
	}
}

func assistantText(item *chatItem) string {
	if item == nil {
		return ""
	}
	var b strings.Builder
	for _, seg := range item.Segments {
		if seg.Kind == segText {
			b.WriteString(seg.Text)
		}
	}
	return b.String()
}

func (m *shellModel) appendReasoning(text string) {
	text = textutil.Valid(text)
	if text == "" {
		return
	}
	m.assistantItem(time.Now()).appendReasoning(text)
	m.syncViewport()
}

func (m *shellModel) startTool(ev bus.Event) {
	item := m.assistantItem(eventTime(ev))
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
		if ev.ToolCallID != "" {
			m.startTool(ev)
			ref, ok = m.toolItems[ev.ToolCallID]
		}
		if !ok {
			return
		}
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
}

func (m *shellModel) handleApprovalKey(key string) bool {
	if len(m.approvals) == 0 {
		return false
	}
	opts := approvalOptions()
	switch key {
	case "j", "down", "tab":
		m.approvalPick = (m.approvalPick + 1) % len(opts)
		return true
	case "k", "up", "shift+tab":
		m.approvalPick = (m.approvalPick - 1 + len(opts)) % len(opts)
		return true
	case "1", "2", "3":
		idx := int(key[0] - '1')
		if idx < len(opts) {
			m.approvalPick = idx
			m.resolveApproval(opts[idx].value)
		}
		return true
	case "enter":
		m.resolveApproval(opts[m.approvalPick].value)
		return true
	case "esc", "n":
		m.resolveApproval(tool.Denied)
		return true
	case "y":
		m.resolveApproval(tool.AllowOnce)
		return true
	case "a":
		m.resolveApproval(tool.AllowAlways)
		return true
	}
	return false
}

func (m *shellModel) resolveApproval(v tool.Approval) {
	if len(m.approvals) == 0 {
		return
	}
	req := m.approvals[0]
	req.resp <- v
	m.dropApproval(req.id)
	m.approvalPick = 0
}

func (m *shellModel) syncLayout() {
	if m.width <= 0 || m.height <= 0 {
		return
	}
	if cmd := m.refreshSuggestions(); cmd != nil {
		m.pendingCmd = tea.Batch(m.pendingCmd, cmd)
	}
	inWidth := max(20, m.width)
	header := shellHeaderHeight
	status := 1
	if len(m.approvals) > 0 {
		status = viewHeight(m.renderStatus())
	}
	footer := 1
	fixed := header + status + footer
	room := max(1, m.height-fixed)
	m.input.SetWidth(inWidth)
	if m.focus == focusComposer {
		_ = m.input.Focus()
	} else {
		m.input.Blur()
	}
	m.suggestRows = min(len(m.suggests), 6)
	for {
		suggestions := m.suggestionHeight()
		wantComposer := clamp(len(strings.Split(textutil.Valid(m.input.Value()), "\n"))+1, 2, 7)
		composer := min(wantComposer, max(1, room-suggestions))
		m.input.SetHeight(composer)
		if fixed+m.composerHeight() <= m.height || m.suggestRows == 0 {
			break
		}
		m.suggestRows--
	}
	nextWidth := max(20, m.width)
	nextHeight := max(0, m.height-fixed-m.composerHeight())
	if m.viewport.Width != nextWidth || m.viewport.Height != nextHeight {
		m.viewport.Width = nextWidth
		m.viewport.Height = nextHeight
		m.syncViewport()
		return
	}
	m.viewport.Width = nextWidth
	m.viewport.Height = nextHeight
}

func (m *shellModel) suggestionHeight() int {
	if len(m.suggests) == 0 || m.suggestRows == 0 {
		return 0
	}
	return min(len(m.suggests), m.suggestRows) + 1
}

func (m *shellModel) composerHeight() int {
	return viewHeight(m.renderComposer())
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
		off := max(0, span.End-m.viewport.Height)
		m.viewport.SetYOffset(off)
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

func (m *shellModel) statusLineCmd(force bool) tea.Cmd {
	if m == nil || m.app == nil || m.statusBusy {
		return nil
	}
	script := strings.TrimSpace(m.app.Config().StatusLine)
	if script == "" {
		m.statusLine = ""
		return nil
	}
	if !force && !m.statusAt.IsZero() && time.Since(m.statusAt) < 2*time.Second {
		return nil
	}
	m.statusBusy = true
	return func() tea.Msg {
		return shellStatusLineMsg{Text: execStatusScript(script)}
	}
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
	rt := "native"
	model := ""
	ws := "."
	sid := "(new)"
	if m == nil || m.app == nil {
		return cliState{Runtime: rt, Model: model, Cwd: ws, Session: sid}
	}
	rt = strings.TrimSpace(m.app.Config().Runtime)
	if rt == "" {
		rt = "native"
	}
	model = m.app.CurrentModel()
	switch rt {
	case "claude":
		model = "claude native"
	case "codex":
		model = "codex native"
	}
	ws = strings.TrimSpace(m.app.Workspace())
	if ws == "" {
		ws = "."
	}
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
