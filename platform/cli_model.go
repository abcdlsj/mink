package platform

import (
	"context"
	"fmt"
	"sort"
	"strings"

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
	agentID string
	name    string
	args    string
	line    int
	done    bool
	err     string
	spinner spinner.Model
}

type delegationState struct {
	id     string
	from   string
	to     string
	desc   string
	status string
	detail string
	done   bool
}

type model struct {
	cli         *CLI
	input       textarea.Model
	output      []string
	agents      map[string]*agentState
	agentKeys   []string
	tools       map[string]*toolState
	toolLog     []string
	delegations map[string]*delegationState
	delegateIDs []string
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
	mainWidth       int
	sidebarWidth    int
	showSidebar     bool
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
		m.resizeInput()
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

	layout := m.computeLayout()
	if layout.showSidebar {
		return m.renderWideView(layout)
	}

	var b strings.Builder
	confirming := m.isConfirming()
	confirmCmd := m.confirmCommand()

	m.writeMainHeader(&b, layout.mainWidth)
	m.writeOutputSection(&b, layout.outputHeight)
	m.writeAgentSection(&b, layout.agentDetailLine)
	m.writeConfirmSection(&b, confirming, confirmCmd)
	m.writeInputSection(&b, confirming)

	return b.String()
}

func (m *model) renderWideView(layout layoutMetrics) string {
	var left strings.Builder
	confirming := m.isConfirming()
	confirmCmd := m.confirmCommand()

	m.writeMainHeader(&left, layout.mainWidth)
	m.writeOutputSection(&left, layout.outputHeight)
	m.writeConfirmSection(&left, confirming, confirmCmd)
	m.writeInputSection(&left, confirming)

	leftPane := lipgloss.NewStyle().Width(layout.mainWidth).MaxWidth(layout.mainWidth).Render(left.String())
	sidebar := m.renderSidebar(layout.sidebarWidth)
	sep := styleDim.Render("│")
	return lipgloss.JoinHorizontal(lipgloss.Top, leftPane, sep, sidebar)
}

func (m *model) writeOutputSection(b *strings.Builder, outputHeight int) {
	for _, line := range m.visibleOutput(outputHeight) {
		b.WriteString(line)
		b.WriteString("\n")
	}
}

func (m *model) writeMainHeader(b *strings.Builder, width int) {
	title := styleSidebarBadge.Render("MINK") + " " + styleBold.Render("multi-agent console")
	b.WriteString(title)
	b.WriteString("\n")

	if m.cli.statusFn != nil {
		status := m.cli.statusFn()
		var parts []string
		if status.Model != "" {
			parts = append(parts, status.Model)
		}
		parts = append(parts, fmt.Sprintf("↑%s ↓%s", fmtTokens(status.TokenIn), fmtTokens(status.TokenOut)))
		if status.Session != "" {
			parts = append(parts, truncate(status.Session, 14))
		}
		if status.Workspace != "" {
			parts = append(parts, truncate(status.Workspace, max(width/3, 12)))
		}
		b.WriteString(styleMutedBlock.Render(strings.Join(parts, "  │  ")))
		b.WriteString("\n")
	} else {
		b.WriteString(styleMutedBlock.Render("agentic workspace"))
		b.WriteString("\n")
	}

	barWidth := max(width-2, 12)
	b.WriteString(styleBar.Render(strings.Repeat("─", barWidth)))
	b.WriteString("\n")
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
	b.WriteString(styleConfirmBanner.Render("!! CONFIRMATION PENDING"))
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
	if !m.useSidebar() {
		if bar := m.statusBar(); bar != "" {
			b.WriteString(bar)
			b.WriteString("\n")
		}
	}
	if m.pending > 0 {
		b.WriteString(m.spinner.View())
		b.WriteString(" ")
	}
	if confirming {
		b.WriteString(stylePromptDanger.Render("!! "))
	} else {
		b.WriteString(stylePrompt.Render("» "))
	}
	b.WriteString(m.input.View())
}

func (m *model) renderSidebar(width int) string {
	innerWidth := max(width-4, 1)
	var sections []string
	if m.cli.statusFn != nil {
		status := m.cli.statusFn()
		sections = append(sections, m.renderOverviewSection(status, innerWidth))
		sections = append(sections, m.renderRosterSection(status, innerWidth))
	}
	sections = append(sections, m.renderCollabSection(innerWidth))
	sections = append(sections, m.renderToolSection(innerWidth))
	sections = append(sections, m.renderShortcutSection(innerWidth))

	var filtered []string
	for _, section := range sections {
		if strings.TrimSpace(section) != "" {
			filtered = append(filtered, section)
		}
	}
	content := lipgloss.NewStyle().Width(innerWidth).Render(strings.Join(filtered, "\n\n"))
	return styleSidebarFrame.Width(width - 4).Render(content)
}

func (m *model) renderOverviewSection(status StatusInfo, width int) string {
	var lines []string
	header := styleSidebarBadge.Render("MINK") + " " + styleBold.Render("CLI")
	lines = append(lines, header)
	if status.Model != "" {
		lines = append(lines, fmt.Sprintf("%s %s", styleSidebarLabel.Render("Model"), styleSidebarValue.Render(status.Model)))
	}
	lines = append(lines, fmt.Sprintf("%s %s", styleSidebarLabel.Render("Tokens"), styleSidebarValue.Render(fmt.Sprintf("↑%s ↓%s", fmtTokens(status.TokenIn), fmtTokens(status.TokenOut)))))
	if status.Session != "" {
		lines = append(lines, fmt.Sprintf("%s %s", styleSidebarLabel.Render("Session"), styleSidebarValue.Render(truncate(status.Session, max(width-10, 8)))))
	}
	if status.Workspace != "" {
		lines = append(lines, fmt.Sprintf("%s %s", styleSidebarLabel.Render("Workspace"), styleSidebarValue.Render(truncate(status.Workspace, max(width-12, 8)))))
	}
	lines = append(lines, fmt.Sprintf("%s %s", styleSidebarLabel.Render("Turn"), styleSidebarValue.Render(m.turnLabel())))
	return m.renderSidebarSection("Overview", lines)
}

func (m *model) renderRosterSection(status StatusInfo, width int) string {
	if len(status.Agents) == 0 {
		return m.renderSidebarSection("Agents", []string{styleDim.Render("No registered agents")})
	}
	var lines []string
	limit := min(len(status.Agents), sidebarAgents)
	for i := 0; i < limit; i++ {
		agent := status.Agents[i]
		name := agent.Name
		if name == "" {
			name = agent.ID
		}
		line := fmt.Sprintf("%s %s %s", m.registryStatusGlyph(agent.Status), styleAgent.Render(truncate(name, max(width-12, 8))), styleDim.Render(fmt.Sprintf("runs:%d", agent.Runs)))
		lines = append(lines, line)
		if len(agent.Caps) > 0 {
			lines = append(lines, styleDim.Render("  "+truncate(strings.Join(agent.Caps, " · "), max(width-2, 8))))
		}
	}
	if len(status.Agents) > limit {
		lines = append(lines, styleDim.Render(fmt.Sprintf("+%d more", len(status.Agents)-limit)))
	}
	return m.renderSidebarSection("Agent Network", lines)
}

func (m *model) renderCollabSection(width int) string {
	var lines []string
	for _, id := range m.agentKeys {
		agent := m.agents[id]
		if agent == nil {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s %s", m.agentStatus(agent), truncate(agent.task, max(width-4, 8))))
		details := m.agentDetail(agent, 2)
		for _, detail := range details {
			lines = append(lines, styleDim.Render("  "+truncate(stripANSI(detail), max(width-2, 8))))
		}
	}
	for _, id := range m.recentDelegations(sidebarDelegates) {
		d := m.delegations[id]
		if d == nil {
			continue
		}
		title := truncate(d.desc, max(width-4, 8))
		if d.to != "" {
			title = truncate(d.to+" · "+title, max(width-4, 8))
		}
		lines = append(lines, fmt.Sprintf("%s %s", m.delegationGlyph(d), title))
		if d.detail != "" {
			lines = append(lines, styleDim.Render("  "+truncate(d.detail, max(width-2, 8))))
		}
	}
	if len(lines) == 0 {
		lines = append(lines, styleDim.Render("No live collaboration"))
	}
	return m.renderSidebarSection("Collaboration", lines)
}

func (m *model) renderToolSection(width int) string {
	var lines []string
	for _, ts := range m.activeTools() {
		lines = append(lines, fmt.Sprintf("%s %s", ts.spinner.View(), truncate(ts.name+" "+ts.args, max(width-4, 8))))
	}
	for _, entry := range m.toolLog {
		lines = append(lines, truncate(entry, max(width, 8)))
		if len(lines) >= sidebarTools {
			break
		}
	}
	if len(lines) == 0 {
		lines = append(lines, styleDim.Render("No recent tool activity"))
	}
	return m.renderSidebarSection("Tools", lines)
}

func (m *model) renderShortcutSection(width int) string {
	lines := []string{
		styleKeycap.Render("Enter") + " send",
		styleKeycap.Render("Ctrl+J") + " newline",
		styleKeycap.Render("PgUp/PgDn") + " scroll",
		styleKeycap.Render("Esc") + " interrupt",
		styleKeycap.Render("!agents") + " roster",
	}
	for i := range lines {
		lines[i] = truncate(lines[i], max(width, 8))
	}
	return m.renderSidebarSection("Keys", lines)
}

func (m *model) renderSidebarSection(title string, lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return styleSectionTitle.Render(title) + "\n" + strings.Join(lines, "\n")
}

func (m *model) turnLabel() string {
	switch {
	case m.isConfirming():
		return "confirm"
	case m.pending > 0:
		return fmt.Sprintf("running (%d)", m.pending)
	default:
		return "idle"
	}
}

func (m *model) registryStatusGlyph(status string) string {
	switch status {
	case "busy":
		return styleAgentBusy.Render("●")
	case "sleeping":
		return styleAgentSleep.Render("◐")
	case "offline":
		return styleAgentOff.Render("○")
	default:
		return styleAgentIdle.Render("●")
	}
}

func (m *model) delegationGlyph(d *delegationState) string {
	if d == nil {
		return styleDim.Render("·")
	}
	switch d.status {
	case "ok", "done":
		return styleSuccess.Render("✓")
	case "error":
		return styleFail.Render("✗")
	case "accepted":
		return styleAgentBusy.Render("→")
	default:
		return styleDim.Render("…")
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
	showSidebar := m.useSidebar()
	mainWidth := m.mainPaneWidth()
	sidebarWidth := 0
	if showSidebar {
		sidebarWidth = m.sidebarWidth()
	}
	agentDetailLine := m.agentDetailLines()
	nonOutput := m.nonOutputLines(agentDetailLine)
	outputHeight := max(m.height-nonOutput, 1)
	maxScroll := max(len(m.output)-outputHeight, 0)

	return layoutMetrics{
		outputHeight:    outputHeight,
		maxScroll:       maxScroll,
		agentDetailLine: agentDetailLine,
		mainWidth:       mainWidth,
		sidebarWidth:    sidebarWidth,
		showSidebar:     showSidebar,
	}
}

func (m *model) nonOutputLines(agentDetailLine int) int {
	inputHeight := max(m.input.Height(), 1)
	lines := inputHeight + 2 + mainHeaderLines

	if !m.useSidebar() && len(m.agentKeys) > 0 {
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
		w = max(m.mainPaneWidth()-4, 80)
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

func (m *model) resizeInput() {
	width := m.mainPaneWidth()
	if width > 4 {
		m.input.SetWidth(width - 4)
	}
}

func (m *model) useSidebar() bool {
	return m.width >= max(minWideWidth, minMainWidth+sidebarMinWidth+sidebarGap)
}

func (m *model) sidebarWidth() int {
	if !m.useSidebar() {
		return 0
	}
	width := m.width / 3
	if width < sidebarMinWidth {
		width = sidebarMinWidth
	}
	if width > sidebarMaxWidth {
		width = sidebarMaxWidth
	}
	if m.width-width-sidebarGap < minMainWidth {
		width = m.width - minMainWidth - sidebarGap
	}
	return max(width, sidebarMinWidth)
}

func (m *model) mainPaneWidth() int {
	if !m.useSidebar() {
		return max(m.width, minMainWidth)
	}
	return max(m.width-m.sidebarWidth()-sidebarGap, minMainWidth)
}

func (m *model) recordToolLog(line string) {
	if strings.TrimSpace(line) == "" {
		return
	}
	m.toolLog = append([]string{line}, m.toolLog...)
	if len(m.toolLog) > sidebarTools {
		m.toolLog = m.toolLog[:sidebarTools]
	}
}

func (m *model) activeTools() []*toolState {
	var out []*toolState
	for _, ts := range m.tools {
		if ts != nil && !ts.done {
			out = append(out, ts)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].line < out[j].line
	})
	return out
}

func (m *model) upsertDelegation(id string) *delegationState {
	if id == "" {
		return nil
	}
	if m.delegations == nil {
		m.delegations = make(map[string]*delegationState)
	}
	if d, ok := m.delegations[id]; ok {
		return d
	}
	d := &delegationState{id: id}
	m.delegations[id] = d
	m.delegateIDs = append(m.delegateIDs, id)
	if len(m.delegateIDs) > sidebarDelegates*2 {
		drop := len(m.delegateIDs) - sidebarDelegates*2
		for _, oldID := range m.delegateIDs[:drop] {
			delete(m.delegations, oldID)
		}
		m.delegateIDs = m.delegateIDs[drop:]
	}
	return d
}

func (m *model) recentDelegations(limit int) []string {
	n := len(m.delegateIDs)
	if n == 0 {
		return nil
	}
	if n > limit {
		n = limit
	}
	out := make([]string, 0, n)
	for i := len(m.delegateIDs) - 1; i >= 0 && len(out) < n; i-- {
		out = append(out, m.delegateIDs[i])
	}
	return out
}
