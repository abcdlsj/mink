package platform

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/rivo/uniseg"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/command"
	"github.com/abcdlsj/mink/hook"
	"github.com/abcdlsj/mink/tool"
)

type busMsg bus.Msg

type agentState struct {
	id           string
	task         string
	lines        []string
	done         bool
	directOutput bool
	spinner      spinner.Model
}

type toolState struct {
	id      string
	name    string
	args    string
	line    int
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
	agentDetailLine int
}

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

func (m *model) View() string {
	if m.quitting {
		return "Bye!\n"
	}

	var b strings.Builder
	layout := m.computeLayout()
	confirming := m.isConfirming()
	confirmCmd := m.confirmCommand()

	m.writeOutputSection(&b, layout.outputHeight)
	m.writeAgentSection(&b, layout.agentDetailLine)
	m.writeConfirmSection(&b, confirming, confirmCmd)
	m.writeInputSection(&b, confirming)

	return b.String()
}

func (m *model) writeOutputSection(b *strings.Builder, outputHeight int) {
	for _, line := range m.visibleOutput(outputHeight) {
		b.WriteString(line)
		b.WriteString("\n")
	}
}

func (m *model) writeAgentSection(b *strings.Builder, detailLimit int) {
	if len(m.agentKeys) == 0 {
		return
	}

	b.WriteString("\n")
	b.WriteString(styleDim.Render("─── agents ───"))
	b.WriteString("\n")
	for _, id := range m.agentKeys {
		agent := m.agents[id]
		if agent == nil {
			continue
		}
		fmt.Fprintf(b, "%s %s %s\n", m.agentStatus(agent), styleAgent.Render(id), styleDim.Render(agent.task))
		for _, line := range m.agentDetail(agent, detailLimit) {
			b.WriteString("  ")
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	b.WriteString(styleDim.Render("──────────────"))
	b.WriteString("\n")
}

func (m *model) agentStatus(agent *agentState) string {
	if agent.done {
		return styleSuccess.Render("✓")
	}
	return agent.spinner.View()
}

func (m *model) agentDetail(agent *agentState, limit int) []string {
	lines := agent.lines
	if limit >= 0 && len(lines) > limit {
		return lines[len(lines)-limit:]
	}
	return lines
}

func (m *model) writeConfirmSection(b *strings.Builder, confirming bool, confirmCmd string) {
	if !confirming {
		return
	}

	b.WriteString("\n")
	b.WriteString(styleConfirmBanner.Render("⚠ CONFIRMATION PENDING"))
	b.WriteString("\n")
	if confirmCmd != "" {
		b.WriteString(styleConfirmCmd.Render("$ " + confirmCmd))
		b.WriteString("\n")
	}
	b.WriteString(styleConfirmHint.Render("[y] Allow once  [a] Always allow similar  [n] Deny"))
	b.WriteString("\n")
}

func (m *model) writeInputSection(b *strings.Builder, confirming bool) {
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
	mx := m.maxScroll()
	if m.scroll > mx {
		m.scroll = mx
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

func (m *model) scrollToTop()    { m.scroll = m.maxScroll() }
func (m *model) scrollToBottom() { m.scroll = 0 }
func (m *model) pageSize() int   { return m.computeLayout().outputHeight }
func (m *model) maxScroll() int  { return m.computeLayout().maxScroll }

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

func (m *model) computeLayout() layoutMetrics {
	agentDetailLine := m.agentDetailLines()
	nonOutput := m.nonOutputLines(agentDetailLine)
	outputHeight := max(m.height-nonOutput, 1)
	maxScroll := max(len(m.output)-outputHeight, 0)

	return layoutMetrics{
		outputHeight:    outputHeight,
		maxScroll:       maxScroll,
		agentDetailLine: agentDetailLine,
	}
}

func (m *model) nonOutputLines(agentDetailLine int) int {
	inputHeight := max(m.input.Height(), 1)
	lines := inputHeight + 2
	if m.cli.statusFn != nil {
		lines++
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

	return lines
}

func (m *model) agentDetailLines() int {
	limit := agentLineLimit
	for limit > 0 {
		available := m.height - m.nonOutputLines(limit)
		if available >= minOutputLines {
			return limit
		}
		limit--
	}
	return 0
}

func (m *model) refreshInputMode() {
	if m.isConfirming() {
		m.input.Placeholder = "Type y/a/n, then Enter"
		return
	}
	m.input.Placeholder = "Type message... (Enter: submit, Ctrl+J: newline)"
}

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

	m.input.SetHeight(min(visual+1, maxInputHeight))
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

func (m *model) appendToAgent(id string, line string) {
	if agent, exists := m.agents[id]; exists {
		agent.lines = append(agent.lines, line)
		if len(agent.lines) > agentLineLimit {
			agent.lines = agent.lines[len(agent.lines)-agentLineLimit:]
		}
	}
}

func (m *model) isDirectOutput(agentID string) bool {
	if a, ok := m.agents[agentID]; ok {
		return a.directOutput
	}
	return false
}
