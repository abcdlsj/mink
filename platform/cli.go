package platform

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/command"
	"github.com/abcdlsj/mink/hook"
)

const (
	maxOutputLines  = 4000
	agentLineLimit  = 8
	mouseScrollStep = 3
	minOutputLines  = 5
	confirmPromptID = "[confirm]"
)

var (
	stylePrompt        = lipgloss.NewStyle().Foreground(lipgloss.Color("#6C7086")).Bold(true)
	stylePromptDanger  = lipgloss.NewStyle().Foreground(lipgloss.Color("#F38BA8")).Bold(true)
	styleAssist        = lipgloss.NewStyle().Foreground(lipgloss.Color("#A6E3A1"))
	styleTool          = lipgloss.NewStyle().Foreground(lipgloss.Color("#F9E2AF")).Faint(true)
	styleCmd           = lipgloss.NewStyle().Foreground(lipgloss.Color("#CBA6F7")).Faint(true)
	styleSuccess       = lipgloss.NewStyle().Foreground(lipgloss.Color("#94E2D5"))
	styleFail          = lipgloss.NewStyle().Foreground(lipgloss.Color("#F38BA8"))
	styleDim           = lipgloss.NewStyle().Foreground(lipgloss.Color("#585B70"))
	styleAgent         = lipgloss.NewStyle().Foreground(lipgloss.Color("#89B4FA")).Bold(true)
	styleCode          = lipgloss.NewStyle().Foreground(lipgloss.Color("#F5C2E7"))
	styleBold          = lipgloss.NewStyle().Bold(true)
	styleConfirmBanner = lipgloss.NewStyle().Foreground(lipgloss.Color("#1E1E2E")).Background(lipgloss.Color("#F38BA8")).Bold(true).Padding(0, 1)
	styleConfirmCmd    = lipgloss.NewStyle().Foreground(lipgloss.Color("#FAB387")).Bold(true)
	styleConfirmHint   = lipgloss.NewStyle().Foreground(lipgloss.Color("#F9E2AF")).Bold(true)

	ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)
)

type agentState struct {
	id      string
	task    string
	lines   []string
	done    bool
	spinner spinner.Model
}

type busMsg bus.Msg

type CLI struct {
	bus    *bus.Bus
	router *command.Router
	hooks  *hook.Manager
	stop   chan struct{}

	program *tea.Program
	model   *model

	confirmMu sync.Mutex
	confirmCh chan bool
	confirmOn bool
	confirmQ  string
}

type model struct {
	cli       *CLI
	input     textinput.Model
	output    []string
	agents    map[string]*agentState
	agentKeys []string
	quitting  bool
	width     int
	height    int
	pending   int
	spinner   spinner.Model
	streaming bool
	streamBuf strings.Builder
	scroll    int
}

type layoutMetrics struct {
	outputHeight    int
	maxScroll       int
	showScroll      bool
	agentDetailLine int
}

func NewCLI(b *bus.Bus, r *command.Router, h *hook.Manager) *CLI {
	return &CLI{
		bus:    b,
		router: r,
		hooks:  h,
		stop:   make(chan struct{}),
	}
}

func (c *CLI) ID() string { return "cli" }

func (c *CLI) Start(ctx context.Context) error {
	c.subscribeMessages(ctx)
	return nil
}

func (c *CLI) Run() error {
	ti := textinput.New()
	ti.Placeholder = "Type your message..."
	ti.Prompt = ""
	ti.Focus()
	ti.Width = 80

	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = styleDim

	m := &model{
		cli:       c,
		input:     ti,
		output:    []string{styleDim.Render("mink. type 'exit' to quit")},
		agents:    make(map[string]*agentState),
		agentKeys: []string{},
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
	c.bus.Subscribe(bus.TypeCommand, ch)
	c.bus.Subscribe(bus.TypeCommandOK, ch)
	c.bus.Subscribe(bus.TypeCommandError, ch)
	c.bus.Subscribe(bus.TypeAgentSpawn, ch)
	c.bus.Subscribe(bus.TypeAgentDone, ch)
	c.bus.Subscribe(bus.TypeTaskStart, ch)
	c.bus.Subscribe(bus.TypeTaskDone, ch)
	c.bus.Subscribe(bus.TypeStreamChunk, ch)
	c.bus.Subscribe(bus.TypeStreamEnd, ch)
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

func (c *CLI) Allow(ctx context.Context, raw string) (bool, error) {
	c.confirmMu.Lock()
	if c.confirmOn {
		c.confirmMu.Unlock()
		return false, fmt.Errorf("another confirmation is in progress")
	}

	ch := make(chan bool, 1)
	c.confirmOn = true
	c.confirmCh = ch
	c.confirmQ = raw
	c.confirmMu.Unlock()

	if c.program != nil {
		c.program.Send(confirmRequestMsg(raw))
	}

	select {
	case ok := <-ch:
		return ok, nil
	case <-ctx.Done():
		c.confirmMu.Lock()
		if c.confirmOn && c.confirmCh == ch {
			c.confirmOn = false
			c.confirmCh = nil
			c.confirmQ = ""
		}
		c.confirmMu.Unlock()
		return false, ctx.Err()
	}
}

type confirmRequestMsg string

func (m *model) Init() tea.Cmd {
	return textinput.Blink
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
		case tea.KeyUp:
			m.scrollUp(1)
			return m, nil
		case tea.KeyDown:
			m.scrollDown(1)
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
		switch {
		case msg.Button == tea.MouseButtonWheelUp || msg.Type == tea.MouseWheelUp:
			m.scrollUp(mouseScrollStep)
			return m, nil
		case msg.Button == tea.MouseButtonWheelDown || msg.Type == tea.MouseWheelDown:
			m.scrollDown(mouseScrollStep)
			return m, nil
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		if m.width > 4 {
			m.input.Width = msg.Width - 4
		}

	case busMsg:
		return m.handleBusMsg(bus.Msg(msg))

	case confirmRequestMsg:
		m.appendOutput("")
		m.appendOutput(styleConfirmBanner.Render("⚠ DANGEROUS COMMAND CONFIRMATION REQUIRED"))
		m.appendOutput(styleConfirmCmd.Render("$ " + string(msg)))
		m.appendOutput(styleConfirmHint.Render("Input required: yes/y to approve, no/n to cancel, then press Enter"))
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
		if len(cmds) > 0 {
			return m, tea.Batch(cmds...)
		}
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
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
	m.input.SetValue("")

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

	m.appendOutput(stylePrompt.Render("› ") + text)

	ctx := command.WithSource(context.Background(), bus.AddrPlatformCLI)

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
	m.cli.confirmMu.Unlock()

	if !active || ch == nil {
		return false
	}

	ans := strings.ToLower(strings.TrimSpace(text))
	if ans != "y" && ans != "yes" && ans != "n" && ans != "no" {
		m.appendOutput(styleConfirmHint.Render("confirmation pending: type yes/y or no/n and press Enter"))
		return true
	}

	ok := ans == "y" || ans == "yes"
	select {
	case ch <- ok:
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

	if ok {
		m.appendOutput(styleSuccess.Render("✓ command approved"))
	} else {
		m.appendOutput(styleFail.Render("✗ command cancelled"))
	}
	return true
}

func (m *model) handleBusMsg(msg bus.Msg) (tea.Model, tea.Cmd) {
	if msg.To != bus.AddrBroadcast && msg.To != bus.AddrPlatformCLI {
		return m, nil
	}

	isSubAgent := msg.From != "" && msg.From != bus.AddrAgentMain && msg.From != bus.AddrSystemSup

	switch msg.Type {
	case bus.TypeAgentSpawn:
		return m.handleAgentSpawn(msg)

	case bus.TypeAgentDone:
		return m.handleAgentDone(msg)

	case bus.TypeAssistant:
		if isSubAgent {
			m.appendToAgent(msg.From, renderMarkdown(truncate(fmt.Sprintf("%v", msg.Payload), 160)))
		} else {
			m.appendOutput("")
			m.appendOutput(renderMarkdown(fmt.Sprintf("%v", msg.Payload)))
		}

	case bus.TypeToolCall:
		line := styleTool.Render("◉ " + truncate(fmt.Sprintf("%v", msg.Payload), 120))
		if isSubAgent {
			m.appendToAgent(msg.From, line)
		} else {
			m.appendOutput(line)
		}

	case bus.TypeToolResult:
		line := styleSuccess.Render("✓ done")
		if isSubAgent {
			m.appendToAgent(msg.From, line)
		} else {
			m.appendOutput(line)
		}

	case bus.TypeToolError:
		line := styleFail.Render("✗ " + truncate(fmt.Sprintf("%v", msg.Payload), 160))
		if isSubAgent {
			m.appendToAgent(msg.From, line)
		} else {
			m.appendOutput(line)
		}

	case bus.TypeCommand:
		line := styleCmd.Render("$ " + fmt.Sprintf("%v", msg.Payload))
		if isSubAgent {
			m.appendToAgent(msg.From, line)
		} else {
			m.appendOutput(line)
		}

	case bus.TypeCommandOK:
		line := styleSuccess.Render("✓ done")
		if isSubAgent {
			m.appendToAgent(msg.From, line)
		} else {
			m.appendOutput(line)
		}

	case bus.TypeCommandError:
		line := styleFail.Render("✗ " + truncate(fmt.Sprintf("%v", msg.Payload), 160))
		if isSubAgent {
			m.appendToAgent(msg.From, line)
		} else {
			m.appendOutput(line)
		}

	case bus.TypeTurnDone:
		if msg.From == bus.AddrAgentMain {
			if m.pending > 0 {
				m.pending--
			}
			m.agentKeys = []string{}
			m.agents = make(map[string]*agentState)
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
		delta, _ := msg.Payload.(string)
		if delta == "" {
			break
		}
		if m.streaming {
			m.streamBuf.WriteString(delta)
			if len(m.output) > 0 {
				m.output[len(m.output)-1] = renderMarkdown(m.streamBuf.String())
			}
		} else {
			m.streaming = true
			m.streamBuf.Reset()
			m.streamBuf.WriteString(delta)
			m.appendOutput("")
			m.appendOutput(renderMarkdown(delta))
		}

	case bus.TypeStreamEnd:
		m.streaming = false
		m.streamBuf.Reset()

	case bus.TypeSessionNew:
		if id, ok := msg.Payload.(string); ok {
			m.appendOutput(styleDim.Render(fmt.Sprintf("[Session] %s", id)))
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
		id:      id,
		task:    task,
		lines:   []string{},
		spinner: s,
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

func (m *model) appendToAgent(id string, line string) {
	if agent, exists := m.agents[id]; exists {
		agent.lines = append(agent.lines, line)
		if len(agent.lines) > agentLineLimit {
			agent.lines = agent.lines[len(agent.lines)-agentLineLimit:]
		}
	}
}

func (m *model) appendOutput(line string) {
	wasAtBottom := m.scroll == 0
	m.output = append(m.output, line)
	if len(m.output) > maxOutputLines {
		drop := len(m.output) - maxOutputLines
		m.output = m.output[drop:]
		if m.scroll > drop {
			m.scroll -= drop
		} else {
			m.scroll = 0
		}
	}
	if wasAtBottom {
		m.scrollToBottom()
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
	if outputHeight > len(m.output) {
		outputHeight = len(m.output)
	}

	scroll := m.scroll
	maxScroll := m.maxScroll()
	if scroll > maxScroll {
		scroll = maxScroll
	}

	end := len(m.output) - scroll
	if end < 0 {
		end = 0
	}
	start := end - outputHeight
	if start < 0 {
		start = 0
	}
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

	currentScroll := m.scroll
	if currentScroll > layout.maxScroll {
		currentScroll = layout.maxScroll
	}

	if layout.showScroll {
		b.WriteString(styleDim.Render(fmt.Sprintf("[scroll %d/%d] ↑/↓ PgUp/PgDn Home/End MouseWheel", currentScroll, layout.maxScroll)))
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
		b.WriteString(styleConfirmHint.Render("Type yes/y to approve, or no/n to cancel, then press Enter"))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	if m.pending > 0 {
		b.WriteString(m.spinner.View())
		b.WriteString(" ")
	}
	if confirming {
		b.WriteString(stylePromptDanger.Render("⚠ "))
	} else {
		b.WriteString(stylePrompt.Render("› "))
	}
	b.WriteString(m.input.View())

	return b.String()
}

func renderMarkdown(s string) string {
	if s == "" {
		return ""
	}

	lines := strings.Split(s, "\n")
	inCode := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inCode = !inCode
			if inCode {
				lang := strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
				if lang != "" {
					lines[i] = styleDim.Render("┌─ " + lang)
				} else {
					lines[i] = styleDim.Render("┌─ code")
				}
			} else {
				lines[i] = styleDim.Render("└─")
			}
			continue
		}

		if inCode {
			lines[i] = styleCode.Render(line)
			continue
		}

		if strings.HasPrefix(trimmed, "# ") {
			lines[i] = styleBold.Render(strings.TrimPrefix(trimmed, "# "))
			continue
		}
		if strings.HasPrefix(trimmed, "## ") {
			lines[i] = styleBold.Render(strings.TrimPrefix(trimmed, "## "))
			continue
		}
		if strings.HasPrefix(trimmed, "### ") {
			lines[i] = styleBold.Render(strings.TrimPrefix(trimmed, "### "))
			continue
		}

		lines[i] = renderInlineMarkdown(line)
	}

	return strings.Join(lines, "\n")
}

func (m *model) refreshInputMode() {
	if m.isConfirming() {
		m.input.Placeholder = "Type yes/y or no/n, then Enter"
		return
	}
	m.input.Placeholder = "Type your message..."
}

func (m *model) computeLayout() layoutMetrics {
	agentDetailLine := m.agentDetailLines()
	outputHeight := m.height - m.nonOutputLines(false, agentDetailLine)
	if outputHeight < 0 {
		outputHeight = 0
	}

	maxScroll := len(m.output) - outputHeight
	if maxScroll < 0 {
		maxScroll = 0
	}

	showScroll := false
	if maxScroll > 0 {
		withScrollHint := m.height - m.nonOutputLines(true, agentDetailLine)
		if withScrollHint < 0 {
			withScrollHint = 0
		}

		if withScrollHint != outputHeight {
			outputHeight = withScrollHint
			maxScroll = len(m.output) - outputHeight
			if maxScroll < 0 {
				maxScroll = 0
			}
		}

		showScroll = maxScroll > 0
	}

	return layoutMetrics{
		outputHeight:    outputHeight,
		maxScroll:       maxScroll,
		showScroll:      showScroll,
		agentDetailLine: agentDetailLine,
	}
}

func (m *model) nonOutputLines(includeScrollHint bool, agentDetailLine int) int {
	lines := 2

	if len(m.agentKeys) > 0 {
		lines += 3
		for _, id := range m.agentKeys {
			agent := m.agents[id]
			if agent == nil {
				continue
			}
			detail := len(agent.lines)
			if detail > agentDetailLine {
				detail = agentDetailLine
			}
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
