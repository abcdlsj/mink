package platform

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/rivo/uniseg"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/command"
	"github.com/abcdlsj/mink/hook"
	"github.com/abcdlsj/mink/tool"
)

const (
	maxOutputLines  = 4000
	agentLineLimit  = 8
	mouseScrollStep = 1
	minOutputLines  = 5
)

var (
	stylePrompt        = lipgloss.NewStyle().Foreground(lipgloss.Color("#6C7086")).Bold(true)
	stylePromptDanger  = lipgloss.NewStyle().Foreground(lipgloss.Color("#F38BA8")).Bold(true)
	styleTool          = lipgloss.NewStyle().Foreground(lipgloss.Color("#F9E2AF")).Faint(true)
	styleSuccess       = lipgloss.NewStyle().Foreground(lipgloss.Color("#94E2D5"))
	styleFail          = lipgloss.NewStyle().Foreground(lipgloss.Color("#F38BA8"))
	styleDim           = lipgloss.NewStyle().Foreground(lipgloss.Color("#585B70"))
	styleBar           = lipgloss.NewStyle().Foreground(lipgloss.Color("#9399B2"))
	styleSession       = lipgloss.NewStyle().Foreground(lipgloss.Color("#A6ADC8"))
	styleAgent         = lipgloss.NewStyle().Foreground(lipgloss.Color("#89B4FA")).Bold(true)
	styleCode          = lipgloss.NewStyle().Foreground(lipgloss.Color("#F5C2E7"))
	styleBold          = lipgloss.NewStyle().Bold(true)
	styleConfirmBanner = lipgloss.NewStyle().Foreground(lipgloss.Color("#1E1E2E")).Background(lipgloss.Color("#F38BA8")).Bold(true).Padding(0, 1)
	styleConfirmCmd    = lipgloss.NewStyle().Foreground(lipgloss.Color("#FAB387")).Bold(true)
	styleConfirmHint   = lipgloss.NewStyle().Foreground(lipgloss.Color("#F9E2AF")).Bold(true)
	styleThinking      = lipgloss.NewStyle().Foreground(lipgloss.Color("#89B4FA"))

	ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)
)

type StatusInfo struct {
	Model     string
	TokenIn   int
	TokenOut  int
	Workspace string
	Session   string
}

type agentState struct {
	id           string
	task         string
	lines        []string
	done         bool
	directOutput bool
	spinner      spinner.Model
}

type busMsg bus.Msg

type CLI struct {
	bus      *bus.Bus
	router   *command.Router
	hooks    *hook.Manager
	statusFn func() StatusInfo
	stop     chan struct{}

	program *tea.Program
	model   *model

	confirmMu sync.Mutex
	confirmCh chan tool.Approval
	confirmOn bool
	confirmQ  string
}

type toolState struct {
	id      string
	name    string
	args    string
	line    int // output line index
	done    bool
	err     string
	spinner spinner.Model
}

type model struct {
	cli         *CLI
	input       textarea.Model
	output      []string
	agents      map[string]*agentState
	agentKeys   []string
	tools       map[string]*toolState
	quitting    bool
	width       int
	height      int
	pending     int
	spinner     spinner.Model
	streaming   bool
	streamBuf   strings.Builder
	streamStart int
	thinking    bool
	thinkingBuf strings.Builder
	thinkStart  int
	scroll      int
}

type layoutMetrics struct {
	outputHeight    int
	maxScroll       int
	showScroll      bool
	agentDetailLine int
}

func NewCLI(b *bus.Bus, r *command.Router, h *hook.Manager, sf func() StatusInfo) *CLI {
	return &CLI{
		bus:      b,
		router:   r,
		hooks:    h,
		statusFn: sf,
		stop:     make(chan struct{}),
	}
}

func (c *CLI) ID() string { return "cli" }

func (c *CLI) Start(ctx context.Context) error {
	c.subscribeMessages(ctx)
	return nil
}

func (c *CLI) Run() error {
	ta := textarea.New()
	ta.Placeholder = "Type message... (Enter: submit, Ctrl+J: newline)"
	ta.Prompt = ""
	ta.ShowLineNumbers = false
	ta.SetWidth(80)
	ta.SetHeight(2)
	ta.EndOfBufferCharacter = ' '
	ta.CharLimit = 0
	ta.Focus()

	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = styleDim

	m := &model{
		cli:       c,
		input:     ta,
		output:    []string{styleDim.Render("mink. type 'exit' to quit")},
		agents:    make(map[string]*agentState),
		agentKeys: []string{},
		tools:     make(map[string]*toolState),
		spinner:   s,
	}
	c.model = m

	c.program = tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseAllMotion())
	_, err := c.program.Run()
	return err
}

func (c *CLI) Stop() error {
	select {
	case <-c.stop:
	default:
		close(c.stop)
	}
	if c.program != nil {
		c.program.Quit()
	}
	return nil
}

func (c *CLI) subscribeMessages(ctx context.Context) {
	ch := make(chan bus.Msg, 64)
	c.bus.Subscribe(bus.TypeAssistant, ch)
	c.bus.Subscribe(bus.TypeTurnDone, ch)
	c.bus.Subscribe(bus.TypeToolCall, ch)
	c.bus.Subscribe(bus.TypeToolResult, ch)
	c.bus.Subscribe(bus.TypeToolError, ch)
	c.bus.Subscribe(bus.TypeAgentSpawn, ch)
	c.bus.Subscribe(bus.TypeAgentDone, ch)
	c.bus.Subscribe(bus.TypeTaskStart, ch)
	c.bus.Subscribe(bus.TypeTaskDone, ch)
	c.bus.Subscribe(bus.TypeStreamChunk, ch)
	c.bus.Subscribe(bus.TypeStreamEnd, ch)
	c.bus.Subscribe(bus.TypeThinkingChunk, ch)
	c.bus.Subscribe(bus.TypeThinkingEnd, ch)
	c.bus.Subscribe(bus.TypeSessionNew, ch)
	c.bus.Subscribe(bus.TypeSessionCompact, ch)

	go func() {
		for {
			select {
			case m := <-ch:
				if c.program != nil {
					c.program.Send(busMsg(m))
				}
			case <-c.stop:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (c *CLI) Approve(ctx context.Context, raw string) (tool.Approval, error) {
	c.confirmMu.Lock()
	if c.confirmOn {
		c.confirmMu.Unlock()
		return tool.Denied, fmt.Errorf("another confirmation is in progress")
	}

	ch := make(chan tool.Approval, 1)
	c.confirmOn = true
	c.confirmCh = ch
	c.confirmQ = raw
	c.confirmMu.Unlock()

	if c.program != nil {
		c.program.Send(confirmRequestMsg(raw))
	}

	select {
	case a := <-ch:
		return a, nil
	case <-ctx.Done():
		c.confirmMu.Lock()
		if c.confirmOn && c.confirmCh == ch {
			c.confirmOn = false
			c.confirmCh = nil
			c.confirmQ = ""
		}
		c.confirmMu.Unlock()
		return tool.Denied, ctx.Err()
	}
}

type confirmRequestMsg string

func (m *model) Init() tea.Cmd {
	return textarea.Blink
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	m.refreshInputMode()

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC:
			m.quitting = true
			return m, tea.Quit
		case tea.KeyEscape:
			return m.handleInterrupt()
		case tea.KeyEnter:
			return m.handleSubmit()
		case tea.KeyCtrlJ:
			m.input, _ = m.input.Update(tea.KeyMsg{Type: tea.KeyEnter})
			m.updateInputHeight()
			return m, nil
		case tea.KeyPgUp:
			m.scrollUp(m.pageSize())
			return m, nil
		case tea.KeyPgDown:
			m.scrollDown(m.pageSize())
			return m, nil
		case tea.KeyHome:
			m.scrollToTop()
			return m, nil
		case tea.KeyEnd:
			m.scrollToBottom()
			return m, nil
		}

	case tea.MouseMsg:
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			m.scrollUp(mouseScrollStep)
			return m, nil
		case tea.MouseButtonWheelDown:
			m.scrollDown(mouseScrollStep)
			return m, nil
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		if m.width > 4 {
			m.input.SetWidth(msg.Width - 4)
		}
		m.updateInputHeight()

	case busMsg:
		return m.handleBusMsg(bus.Msg(msg))

	case confirmRequestMsg:
		m.refreshInputMode()
		return m, nil

	case spinner.TickMsg:
		if m.pending > 0 {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			cmds = append(cmds, cmd)
		}
		for _, agent := range m.agents {
			if !agent.done {
				var cmd tea.Cmd
				agent.spinner, cmd = agent.spinner.Update(msg)
				cmds = append(cmds, cmd)
			}
		}
		for _, ts := range m.tools {
			if !ts.done && ts.line >= 0 && ts.line < len(m.output) {
				var cmd tea.Cmd
				ts.spinner, cmd = ts.spinner.Update(msg)
				cmds = append(cmds, cmd)
				display := truncate(ts.name+" "+ts.args, 100)
				m.output[ts.line] = ts.spinner.View() + " " + styleTool.Render(display)
			}
		}
		if len(cmds) > 0 {
			return m, tea.Batch(cmds...)
		}
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.updateInputHeight()
	if cmd != nil {
		cmds = append(cmds, cmd)
	}

	if len(cmds) == 0 {
		return m, nil
	}
	return m, tea.Batch(cmds...)
}

func (m *model) handleSubmit() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.input.Value())
	m.input.Reset()
	m.input.SetHeight(2)

	if text == "" {
		return m, nil
	}

	if m.tryHandleConfirm(text) {
		return m, nil
	}

	if text == "exit" {
		m.quitting = true
		return m, tea.Quit
	}

	m.appendOutput(stylePrompt.Render("» ") + text)

	ctx := bus.WithSource(context.Background(), bus.AddrPlatformCLI)

	m.cli.hooks.Trigger(ctx, hook.BeforeInput, text)

	if command.IsCommand(text) {
		out, ok, err := m.cli.router.Route(ctx, text)
		if ok {
			if err != nil {
				m.appendOutput(styleFail.Render("✗ " + err.Error()))
			} else {
				m.appendOutput(renderMarkdown(out))
			}
			m.cli.hooks.Trigger(ctx, hook.AfterInput, text)
			return m, nil
		}
	}

	_ = m.cli.bus.Pub(bus.Msg{
		Type:    bus.TypeUserInput,
		From:    bus.AddrPlatformCLI,
		To:      bus.AddrAgentMain,
		Payload: text,
	})

	m.cli.hooks.Trigger(ctx, hook.AfterInput, text)
	m.pending++
	m.scrollToBottom()
	return m, m.spinner.Tick
}

func (m *model) handleInterrupt() (tea.Model, tea.Cmd) {
	if m.pending == 0 {
		return m, nil
	}

	_ = m.cli.bus.Pub(bus.Msg{
		Type:    bus.TypeInterrupt,
		From:    bus.AddrPlatformCLI,
		To:      bus.AddrAgentMain,
		Payload: "user interrupted",
	})

	m.appendOutput(styleDim.Render("[Interrupted]"))
	return m, nil
}

func (m *model) tryHandleConfirm(text string) bool {
	m.cli.confirmMu.Lock()
	ch := m.cli.confirmCh
	active := m.cli.confirmOn
	cmd := m.cli.confirmQ
	m.cli.confirmMu.Unlock()

	if !active || ch == nil {
		return false
	}

	ans := strings.ToLower(strings.TrimSpace(text))

	var approval tool.Approval
	switch ans {
	case "y", "yes":
		approval = tool.AllowOnce
	case "a", "always":
		approval = tool.AllowAlways
	case "n", "no":
		approval = tool.Denied
	default:
		return true
	}

	select {
	case ch <- approval:
	default:
	}

	m.cli.confirmMu.Lock()
	if m.cli.confirmCh == ch {
		m.cli.confirmOn = false
		m.cli.confirmCh = nil
		m.cli.confirmQ = ""
	}
	m.cli.confirmMu.Unlock()
	m.refreshInputMode()

	display := truncate(cmd, 80)
	switch approval {
	case tool.AllowOnce:
		m.appendOutput(styleSuccess.Render("✓ approved: " + display))
	case tool.AllowAlways:
		m.appendOutput(styleSuccess.Render("✓ approved (always): " + display))
	default:
		m.appendOutput(styleFail.Render("✗ denied: " + display))
	}
	return true
}

func (m *model) handleBusMsg(msg bus.Msg) (tea.Model, tea.Cmd) {
	if msg.To != bus.AddrBroadcast && msg.To != bus.AddrPlatformCLI {
		return m, nil
	}

	isSubAgent := msg.From != "" && msg.From != bus.AddrAgentMain && msg.From != bus.AddrSystemSup
	showInline := !isSubAgent || m.isDirectOutput(msg.From)

	switch msg.Type {
	case bus.TypeAgentSpawn:
		return m.handleAgentSpawn(msg)

	case bus.TypeAgentDone:
		return m.handleAgentDone(msg)

	case bus.TypeAssistant:
		if showInline {
			m.appendOutput("")
			m.appendOutput(renderMarkdown(fmt.Sprintf("%v", msg.Payload)))
		} else {
			m.appendToAgent(msg.From, renderMarkdown(truncate(fmt.Sprintf("%v", msg.Payload), 160)))
		}

	case bus.TypeToolCall:
		payload, ok := msg.Payload.(map[string]string)
		if !ok {
			line := styleTool.Render("◉ " + truncate(fmt.Sprintf("%v", msg.Payload), 120))
			if showInline {
				m.appendOutput(line)
			} else {
				m.appendToAgent(msg.From, line)
			}
			break
		}
		id := payload["id"]
		name := payload["name"]
		args := payload["args"]
		display := truncate(name+" "+args, 100)

		if showInline {
			s := spinner.New()
			s.Spinner = spinner.Dot
			s.Style = styleTool
			lineIdx := len(m.output)
			m.appendOutput(s.View() + " " + styleTool.Render(display))
			m.tools[id] = &toolState{
				id:      id,
				name:    name,
				args:    args,
				line:    lineIdx,
				spinner: s,
			}
			return m, s.Tick
		} else {
			m.appendToAgent(msg.From, styleTool.Render("◉ "+display))
		}

	case bus.TypeToolResult:
		payload, ok := msg.Payload.(map[string]string)
		if !ok {
			if showInline {
				m.appendOutput(styleSuccess.Render("✓ done"))
			} else {
				m.appendToAgent(msg.From, styleSuccess.Render("✓ done"))
			}
			break
		}
		id := payload["id"]
		if ts, exists := m.tools[id]; exists && !ts.done {
			ts.done = true
			if ts.line >= 0 && ts.line < len(m.output) {
				display := truncate(ts.name+" "+ts.args, 100)
				m.output[ts.line] = styleSuccess.Render("✓") + " " + styleTool.Render(display)
			}
		} else if !showInline {
			m.appendToAgent(msg.From, styleSuccess.Render("✓ done"))
		}

	case bus.TypeToolError:
		payload, ok := msg.Payload.(map[string]string)
		if !ok {
			line := styleFail.Render("✗ " + truncate(fmt.Sprintf("%v", msg.Payload), 160))
			if showInline {
				m.appendOutput(line)
			} else {
				m.appendToAgent(msg.From, line)
			}
			break
		}
		id := payload["id"]
		errMsg := payload["error"]
		if ts, exists := m.tools[id]; exists && !ts.done {
			ts.done = true
			ts.err = errMsg
			if ts.line >= 0 && ts.line < len(m.output) {
				display := truncate(ts.name+" "+ts.args, 80)
				m.output[ts.line] = styleFail.Render("✗") + " " + styleTool.Render(display) + " " + styleFail.Render(truncate(errMsg, 40))
			}
		} else if !showInline {
			m.appendToAgent(msg.From, styleFail.Render("✗ "+truncate(errMsg, 160)))
		}

	case bus.TypeTurnDone:
		if msg.From == bus.AddrAgentMain {
			if m.pending > 0 {
				m.pending--
			}
			m.agentKeys = []string{}
			m.agents = make(map[string]*agentState)
			m.tools = make(map[string]*toolState)
			m.streaming = false
			m.streamBuf.Reset()
			m.streamStart = 0
			m.thinking = false
			m.thinkingBuf.Reset()
			m.thinkStart = 0
		}

	case bus.TypeTaskStart:
		if payload, ok := msg.Payload.(map[string]string); ok {
			taskID := payload["task_id"]
			cmdText := truncate(payload["cmd"], 60)
			m.appendOutput(styleTool.Render(fmt.Sprintf("[%s] %s", taskID, cmdText)))
		}

	case bus.TypeTaskDone:
		if payload, ok := msg.Payload.(map[string]string); ok {
			taskID := payload["task_id"]
			status := payload["status"]
			if status == "ok" {
				m.appendOutput(styleSuccess.Render(fmt.Sprintf("✓ [%s] completed", taskID)))
			} else {
				errMsg := truncate(payload["error"], 80)
				m.appendOutput(styleFail.Render(fmt.Sprintf("✗ [%s] %s", taskID, errMsg)))
			}
		}

	case bus.TypeStreamChunk:
		if !showInline {
			break
		}
		delta, _ := msg.Payload.(string)
		if delta == "" {
			break
		}
		if m.streaming {
			m.streamBuf.WriteString(delta)
			m.replaceFrom(m.streamStart, renderMarkdown(m.streamBuf.String()))
		} else {
			m.streaming = true
			m.streamBuf.Reset()
			m.streamBuf.WriteString(delta)
			m.appendOutput("")
			m.streamStart = len(m.output)
			m.replaceFrom(m.streamStart, renderMarkdown(delta))
		}

	case bus.TypeStreamEnd:
		if !showInline {
			break
		}
		m.streaming = false
		m.streamBuf.Reset()

	case bus.TypeThinkingChunk:
		if !showInline {
			break
		}
		delta, _ := msg.Payload.(string)
		if delta == "" {
			break
		}
		thinkLine := func(s string) string {
			return styleThinking.Render("thinking: " + strings.ReplaceAll(s, "\n", " "))
		}
		if m.thinking {
			m.thinkingBuf.WriteString(delta)
			m.replaceFrom(m.thinkStart, thinkLine(m.thinkingBuf.String()))
		} else {
			m.thinking = true
			m.thinkingBuf.Reset()
			m.thinkingBuf.WriteString(delta)
			m.appendOutput("")
			m.thinkStart = len(m.output)
			m.replaceFrom(m.thinkStart, thinkLine(delta))
		}

	case bus.TypeThinkingEnd:
		if !showInline {
			break
		}
		m.thinking = false
		m.thinkingBuf.Reset()

	case bus.TypeSessionNew:
		if id, ok := msg.Payload.(string); ok {
			m.appendOutput(styleSession.Render(fmt.Sprintf("[Session] %s", id)))
		}

	case bus.TypeSessionCompact:
		m.appendOutput(styleTool.Render(fmt.Sprintf("[Compact] %v", msg.Payload)))
	}

	return m, nil
}

func (m *model) handleAgentSpawn(msg bus.Msg) (tea.Model, tea.Cmd) {
	payload, ok := msg.Payload.(map[string]string)
	if !ok {
		return m, nil
	}

	id := payload["agent_id"]
	if id == "" {
		id = msg.From
	}
	task := truncate(payload["task"], 60)

	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = styleAgent

	if _, exists := m.agents[id]; !exists {
		m.agentKeys = append(m.agentKeys, id)
	}
	m.agents[id] = &agentState{
		id:           id,
		task:         task,
		lines:        []string{},
		directOutput: payload["direct_output"] == "true",
		spinner:      s,
	}

	return m, m.agents[id].spinner.Tick
}

func (m *model) handleAgentDone(msg bus.Msg) (tea.Model, tea.Cmd) {
	payload, ok := msg.Payload.(map[string]string)
	if !ok {
		return m, nil
	}

	id := payload["agent_id"]
	if id == "" {
		id = msg.From
	}

	if agent, exists := m.agents[id]; exists {
		agent.done = true
	}

	return m, nil
}

func (m *model) isDirectOutput(agentID string) bool {
	if a, ok := m.agents[agentID]; ok {
		return a.directOutput
	}
	return false
}

func (m *model) appendToAgent(id string, line string) {
	if agent, exists := m.agents[id]; exists {
		agent.lines = append(agent.lines, line)
		if len(agent.lines) > agentLineLimit {
			agent.lines = agent.lines[len(agent.lines)-agentLineLimit:]
		}
	}
}

func (m *model) appendOutput(text string) {
	wasAtBottom := m.scroll == 0
	m.output = append(m.output, strings.Split(text, "\n")...)
	m.trimOutput(wasAtBottom)
}

func (m *model) replaceFrom(start int, text string) {
	wasAtBottom := m.scroll == 0
	if start <= len(m.output) {
		m.output = m.output[:start]
	}
	m.output = append(m.output, strings.Split(text, "\n")...)
	if wasAtBottom {
		m.scroll = 0
	}
}

func (m *model) trimOutput(wasAtBottom bool) {
	if len(m.output) > maxOutputLines {
		drop := len(m.output) - maxOutputLines
		m.output = m.output[drop:]
		if m.scroll > drop {
			m.scroll -= drop
		} else {
			m.scroll = 0
		}
		m.streamStart = max(m.streamStart-drop, 0)
		m.thinkStart = max(m.thinkStart-drop, 0)
	} else if wasAtBottom {
		m.scroll = 0
	}
}

func (m *model) scrollUp(n int) {
	if n <= 0 {
		n = 1
	}
	m.scroll += n
	max := m.maxScroll()
	if m.scroll > max {
		m.scroll = max
	}
}

func (m *model) scrollDown(n int) {
	if n <= 0 {
		n = 1
	}
	m.scroll -= n
	if m.scroll < 0 {
		m.scroll = 0
	}
}

func (m *model) scrollToTop() {
	m.scroll = m.maxScroll()
}

func (m *model) scrollToBottom() {
	m.scroll = 0
}

func (m *model) pageSize() int {
	return m.computeLayout().outputHeight
}

func (m *model) maxScroll() int {
	return m.computeLayout().maxScroll
}

func (m *model) visibleOutput(outputHeight int) []string {
	if len(m.output) == 0 || outputHeight <= 0 {
		return nil
	}
	outputHeight = min(outputHeight, len(m.output))

	scroll := m.scroll
	maxScroll := m.maxScroll()
	if scroll > maxScroll {
		scroll = maxScroll
	}

	end := max(len(m.output)-scroll, 0)
	start := max(end-outputHeight, 0)
	if end < start {
		end = start
	}
	return m.output[start:end]
}

func (m *model) View() string {
	if m.quitting {
		return "Bye!\n"
	}

	var b strings.Builder
	layout := m.computeLayout()

	outputLines := m.visibleOutput(layout.outputHeight)
	for _, line := range outputLines {
		b.WriteString(line)
		b.WriteString("\n")
	}

	currentScroll := min(m.scroll, layout.maxScroll)

	if layout.showScroll {
		b.WriteString(styleDim.Render(fmt.Sprintf("[scroll %d/%d] PgUp/PgDn Home/End MouseWheel", currentScroll, layout.maxScroll)))
		b.WriteString("\n")
	}

	confirming := m.isConfirming()
	confirmCmd := m.confirmCommand()

	if len(m.agentKeys) > 0 {
		b.WriteString("\n")
		b.WriteString(styleDim.Render("─── agents ───"))
		b.WriteString("\n")
		for _, id := range m.agentKeys {
			agent := m.agents[id]
			if agent == nil {
				continue
			}

			status := agent.spinner.View()
			if agent.done {
				status = styleSuccess.Render("✓")
			}

			fmt.Fprintf(&b, "%s %s %s\n", status, styleAgent.Render(id), styleDim.Render(agent.task))
			lines := agent.lines
			if layout.agentDetailLine >= 0 && len(lines) > layout.agentDetailLine {
				lines = lines[len(lines)-layout.agentDetailLine:]
			}
			for _, line := range lines {
				b.WriteString("  ")
				b.WriteString(line)
				b.WriteString("\n")
			}
		}
		b.WriteString(styleDim.Render("──────────────"))
		b.WriteString("\n")
	}

	if confirming {
		b.WriteString("\n")
		b.WriteString(styleConfirmBanner.Render("⚠ CONFIRMATION PENDING"))
		b.WriteString("\n")
		if confirmCmd != "" {
			b.WriteString(styleConfirmCmd.Render("$ "+confirmCmd) + "\n")
		}
		b.WriteString(styleConfirmHint.Render("[y] Allow once  [a] Always allow similar  [n] Deny"))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	if bar := m.statusBar(); bar != "" {
		b.WriteString(bar)
		b.WriteString("\n")
	}
	if m.pending > 0 {
		b.WriteString(m.spinner.View())
		b.WriteString(" ")
	}
	if confirming {
		b.WriteString(stylePromptDanger.Render("⚠ "))
	} else {
		b.WriteString(stylePrompt.Render("» "))
	}
	b.WriteString(m.input.View())

	return b.String()
}

var (
	styleDiffAdd    = lipgloss.NewStyle().Foreground(lipgloss.Color("#A6E3A1")).Bold(true)
	styleDiffDel    = lipgloss.NewStyle().Foreground(lipgloss.Color("#F38BA8")).Bold(true)
	styleDiffHeader = lipgloss.NewStyle().Foreground(lipgloss.Color("#89B4FA")).Bold(true)
	styleDiffHunk   = lipgloss.NewStyle().Foreground(lipgloss.Color("#94E2D5")).Faint(true)
)

func renderMarkdown(s string) string {
	if s == "" {
		return ""
	}

	lines := strings.Split(s, "\n")
	inCode := false
	codeLang := ""
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			if inCode {
				inCode = false
				codeLang = ""
				lines[i] = styleDim.Render("└─")
			} else {
				inCode = true
				codeLang = strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
				if codeLang != "" {
					lines[i] = styleDim.Render("┌─ " + codeLang)
				} else {
					lines[i] = styleDim.Render("┌─ code")
				}
			}
			continue
		}

		if inCode {
			lines[i] = renderCodeLine(line, codeLang)
			continue
		}

		if after, ok := strings.CutPrefix(trimmed, "# "); ok {
			lines[i] = styleBold.Render(after)
			continue
		}
		if after, ok := strings.CutPrefix(trimmed, "## "); ok {
			lines[i] = styleBold.Render(after)
			continue
		}
		if after, ok := strings.CutPrefix(trimmed, "### "); ok {
			lines[i] = styleBold.Render(after)
			continue
		}

		lines[i] = renderInlineMarkdown(line)
	}

	return strings.Join(lines, "\n")
}

func (m *model) refreshInputMode() {
	if m.isConfirming() {
		m.input.Placeholder = "Type y/a/n, then Enter"
		return
	}
	m.input.Placeholder = "Type message... (Enter: submit, Ctrl+J: newline)"
}

const maxInputHeight = 8

func (m *model) updateInputHeight() {
	w := m.input.Width()
	if w <= 0 {
		w = 80
	}
	visual := 0
	for _, line := range strings.Split(m.input.Value(), "\n") {
		n := uniseg.StringWidth(line)
		if n <= w {
			visual++
		} else {
			visual += (n-1)/w + 1
		}
	}
	// +1 keeps viewport tall enough so textarea's internal repositionView
	// never scrolls away the first wrapped line on the same keystroke.
	m.input.SetHeight(min(visual+1, maxInputHeight))
}

func (m *model) computeLayout() layoutMetrics {
	agentDetailLine := m.agentDetailLines()

	// Always reserve space for scroll hint when there might be content
	nonOutput := m.nonOutputLines(true, agentDetailLine)
	outputHeight := max(m.height-nonOutput, 1)
	maxScroll := max(len(m.output)-outputHeight, 0)

	// Show scroll hint when content exceeds visible area
	showScroll := len(m.output) > outputHeight

	return layoutMetrics{
		outputHeight:    outputHeight,
		maxScroll:       maxScroll,
		showScroll:      showScroll,
		agentDetailLine: agentDetailLine,
	}
}

func (m *model) nonOutputLines(includeScrollHint bool, agentDetailLine int) int {
	inputHeight := max(m.input.Height(), 1)
	lines := inputHeight + 2 // input + prompt + spacing
	if m.cli.statusFn != nil {
		lines++ // status bar
	}

	if len(m.agentKeys) > 0 {
		lines += 3
		for _, id := range m.agentKeys {
			agent := m.agents[id]
			if agent == nil {
				continue
			}
			detail := min(len(agent.lines), agentDetailLine)
			lines += 1 + detail
		}
	}

	if m.isConfirming() {
		lines += 3
		if m.confirmCommand() != "" {
			lines++
		}
	}

	if includeScrollHint {
		lines++
	}

	return lines
}

func (m *model) agentDetailLines() int {
	limit := agentLineLimit
	for limit > 0 {
		available := m.height - m.nonOutputLines(false, limit)
		if available >= minOutputLines {
			return limit
		}
		limit--
	}
	return 0
}

func (m *model) isConfirming() bool {
	m.cli.confirmMu.Lock()
	defer m.cli.confirmMu.Unlock()
	return m.cli.confirmOn
}

func (m *model) confirmCommand() string {
	m.cli.confirmMu.Lock()
	defer m.cli.confirmMu.Unlock()
	if !m.cli.confirmOn {
		return ""
	}
	return m.cli.confirmQ
}

func renderCodeLine(line, lang string) string {
	if lang != "diff" && lang != "patch" {
		return styleCode.Render(line)
	}
	trimmed := strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(trimmed, "diff "),
		strings.HasPrefix(trimmed, "+++"),
		strings.HasPrefix(trimmed, "---"):
		return styleDiffHeader.Render(line)
	case strings.HasPrefix(trimmed, "@@"):
		return styleDiffHunk.Render(line)
	case strings.HasPrefix(trimmed, "+"):
		return styleDiffAdd.Render(line)
	case strings.HasPrefix(trimmed, "-"):
		return styleDiffDel.Render(line)
	default:
		return styleCode.Render(line)
	}
}

func renderInlineMarkdown(line string) string {
	line = ansiRE.ReplaceAllString(line, "")

	var b strings.Builder
	r := []rune(line)

	for i := 0; i < len(r); {
		if r[i] == '`' {
			j := i + 1
			for j < len(r) && r[j] != '`' {
				j++
			}
			if j < len(r) {
				b.WriteString(styleCode.Render(string(r[i+1 : j])))
				i = j + 1
				continue
			}
		}

		if i+1 < len(r) && r[i] == '*' && r[i+1] == '*' {
			j := i + 2
			for j+1 < len(r) {
				if r[j] == '*' && r[j+1] == '*' {
					break
				}
				j++
			}
			if j+1 < len(r) {
				b.WriteString(styleBold.Render(string(r[i+2 : j])))
				i = j + 2
				continue
			}
		}

		b.WriteRune(r[i])
		i++
	}

	return b.String()
}

func (m *model) statusBar() string {
	if m.cli.statusFn == nil || m.width == 0 {
		return ""
	}
	s := m.cli.statusFn()
	var parts []string
	if s.Model != "" {
		parts = append(parts, s.Model)
	}
	parts = append(parts, fmt.Sprintf("↑%s ↓%s", fmtTokens(s.TokenIn), fmtTokens(s.TokenOut)))
	if s.Workspace != "" {
		ws := s.Workspace
		if len(ws) > m.width/2 {
			ws = "…" + ws[len(ws)-m.width/2:]
		}
		parts = append(parts, ws)
	}
	if s.Session != "" {
		parts = append(parts, s.Session)
	}

	content := strings.Join(parts, " │ ")
	return styleBar.Render("── " + content + " ──")
}

func fmtTokens(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return "…"
	}
	return string(r[:n-1]) + "…"
}
