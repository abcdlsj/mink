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
	statusCache *StatusInfo
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
	m.statusCache = nil
	m.refreshInputMode()

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+p", "alt+up":
			m.scrollUp(1)
			return m, nil
		case "ctrl+n", "alt+down":
			m.scrollDown(1)
			return m, nil
		case "ctrl+b":
			m.scrollUp(m.pageSize())
			return m, nil
		case "ctrl+f":
			m.scrollDown(m.pageSize())
			return m, nil
		}
		switch msg.Type {
		case tea.KeyCtrlC:
			m.quitting = true
			return m, tea.Quit
		case tea.KeyUp:
			if m.input.Value() == "" {
				m.scrollUp(1)
				return m, nil
			}
		case tea.KeyDown:
			if m.input.Value() == "" {
				m.scrollDown(1)
				return m, nil
			}
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
	if !layout.showSidebar {
		return m.renderMainPane(layout)
	}

	leftPane := lipgloss.NewStyle().Width(layout.mainWidth).MaxWidth(layout.mainWidth).Render(m.renderMainPane(layout))
	sidebar := m.renderSidebar(layout.sidebarWidth)
	sep := styleDim.Render("│")
	return lipgloss.JoinHorizontal(lipgloss.Top, leftPane, sep, sidebar)
}

func (m *model) renderMainPane(layout layoutMetrics) string {
	confirming := m.isConfirming()

	var header strings.Builder
	m.writeMainHeader(&header, layout.mainWidth)

	teamPanel := m.renderTeamPanel(layout.mainWidth)

	var transcript strings.Builder
	m.writeOutputSection(&transcript, layout.outputHeight)

	var input strings.Builder
	m.writeInputSection(&input, confirming)

	var b strings.Builder
	b.WriteString(strings.TrimRight(header.String(), "\n"))
	b.WriteString("\n")
	if teamPanel != "" {
		b.WriteString(teamPanel)
		b.WriteString("\n")
	}
	b.WriteString(strings.TrimRight(transcript.String(), "\n"))

	if !layout.showSidebar && m.hasLiveSidebarContent() {
		b.WriteString("\n")
		b.WriteString(m.renderLiveSection(max(layout.mainWidth-2, 8)))
	}
	if confirm := m.renderConfirmPanel(); confirm != "" {
		b.WriteString("\n")
		b.WriteString(confirm)
	}
	b.WriteString("\n")
	b.WriteString(strings.TrimRight(input.String(), "\n"))
	return b.String()
}

func (m *model) writeOutputSection(b *strings.Builder, outputHeight int) {
	title := "Conversation"
	if team := m.statusInfo().Team; team != nil {
		if team.ActiveThread != nil {
			title = "Transcript"
		} else {
			title = "Team Activity"
		}
	}
	b.WriteString(styleSectionTitle.Render(title))
	b.WriteString("\n")
	lines := m.visibleOutput(outputHeight)
	for i := 0; i < max(outputHeight-len(lines), 0); i++ {
		b.WriteString("\n")
	}
	for _, line := range lines {
		b.WriteString(line)
		b.WriteString("\n")
	}
}

func (m *model) writeMainHeader(b *strings.Builder, width int) {
	title := styleSidebarBadge.Render("MINK") + " " + styleBold.Render("CONSOLE")
	b.WriteString(title)
	b.WriteString("\n")

	status := m.statusInfo()
	if m.cli.statusFn != nil {
		var parts []string
		if status.Team != nil {
			parts = append(parts, status.Team.Name)
			if status.Team.ActiveThread != nil && status.Team.ActiveThread.Title != "" {
				parts = append(parts, status.Team.ActiveThread.Title)
			}
		}
		if status.Model != "" {
			parts = append(parts, status.Model)
		}
		if status.Session != "" && status.Team == nil {
			parts = append(parts, status.Session)
		}
		if len(status.Agents) > 0 {
			parts = append(parts, fmt.Sprintf("%d agents", len(status.Agents)))
		}
		parts = append(parts, fmt.Sprintf("↑%s ↓%s", fmtTokens(status.TokenIn), fmtTokens(status.TokenOut)))
		if len(parts) > 0 {
			b.WriteString(styleMutedBlock.Render(strings.Join(parts, "  │  ")))
			b.WriteString("\n")
		}
	} else {
		b.WriteString(styleMutedBlock.Render("multi-agent workspace"))
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
	b.WriteString(styleBar.Render(strings.Repeat("─", max(m.mainPaneWidth()-2, 12))))
	b.WriteString("\n")
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

func (m *model) renderConfirmPanel() string {
	if !m.isConfirming() {
		return ""
	}
	var b strings.Builder
	m.writeConfirmSection(&b, true, m.confirmCommand())
	return strings.TrimRight(b.String(), "\n")
}

func (m *model) renderSidebar(width int) string {
	innerWidth := max(width-4, 1)
	var sections []string
	status := m.statusInfo()
	if status.Team != nil {
		sections = append(sections, m.renderTeamRail(status.Team, innerWidth))
	} else if m.cli.statusFn != nil {
		sections = append(sections, m.renderRosterSection(status, innerWidth))
	}
	sections = append(sections, m.renderLiveSection(innerWidth))

	var filtered []string
	for _, section := range sections {
		if strings.TrimSpace(section) != "" {
			filtered = append(filtered, section)
		}
	}
	content := lipgloss.NewStyle().Width(innerWidth).Render(strings.Join(filtered, "\n\n"))
	return styleSidebarFrame.Width(width - 4).Render(content)
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
		meta := agent.Status
		if agent.Runs > 0 {
			meta = fmt.Sprintf("%s · %d run", agent.Status, agent.Runs)
			if agent.Runs > 1 {
				meta += "s"
			}
		}
		line := fmt.Sprintf("%s %s %s", m.registryStatusGlyph(agent.Status), styleAgent.Render(truncate(name, max(width-18, 8))), styleDim.Render(meta))
		lines = append(lines, line)
		if agent.Status == "busy" && len(agent.Caps) > 0 {
			lines = append(lines, styleDim.Render("  "+truncate(strings.Join(agent.Caps, " · "), max(width-2, 8))))
		}
	}
	if len(status.Agents) > limit {
		lines = append(lines, styleDim.Render(fmt.Sprintf("+%d more", len(status.Agents)-limit)))
	}
	return m.renderSidebarSection("Agent Network", lines)
}

func (m *model) renderTeamPanel(width int) string {
	team := m.statusInfo().Team
	if team == nil {
		return ""
	}
	if team.ActiveThread != nil {
		return m.renderThreadPanel(team, width)
	}
	return m.renderTeamHome(team, width)
}

func (m *model) renderTeamHome(team *TeamStatus, width int) string {
	var lines []string
	lines = append(lines, styleSectionTitle.Render(strings.ToUpper(team.Name)))
	lines = append(lines, styleMutedBlock.Render(fmt.Sprintf("%s · leader %s", team.Status, team.LeaderID)))
	lines = append(lines, "")
	lines = append(lines, styleSidebarLabel.Render("Latest Summary"))
	lines = append(lines, team.LatestSummary)
	lines = append(lines, "")
	lines = append(lines, styleSidebarLabel.Render("Current Blocker"))
	lines = append(lines, team.CurrentBlocker)
	if len(team.RecentThreads) > 0 {
		lines = append(lines, "")
		lines = append(lines, styleSidebarLabel.Render("Recent Threads"))
		limit := min(len(team.RecentThreads), 3)
		for i := 0; i < limit; i++ {
			thread := team.RecentThreads[i]
			lines = append(lines, fmt.Sprintf("%s · %s", thread.Status, thread.Title))
		}
	}
	if len(team.Members) > 0 {
		lines = append(lines, "")
		lines = append(lines, styleSidebarLabel.Render("Members"))
		limit := min(len(team.Members), 4)
		for i := 0; i < limit; i++ {
			member := team.Members[i]
			lines = append(lines, fmt.Sprintf("%s · %s", member.Name, member.Role))
		}
	}
	return styleFrame.Width(max(width-4, 16)).Render(strings.Join(lines, "\n"))
}

func (m *model) renderThreadPanel(team *TeamStatus, width int) string {
	thread := team.ActiveThread
	if thread == nil {
		return ""
	}
	var sections []string
	sections = append(sections, m.renderInfoPanel("Goal", thread.Goal, width))
	sections = append(sections, m.renderInfoPanel("Current Best Answer", thread.BestAnswer, width))
	sections = append(sections, m.renderInfoPanel("Open Blockers", thread.OpenBlockers, width))
	speaker := team.ActiveSpeaker
	if speaker == "" {
		speaker = "No active speaker"
	}
	sections = append(sections, m.renderInfoPanel("Current Speaker", speaker, width))
	return strings.Join(sections, "\n")
}

func (m *model) renderInfoPanel(title, body string, width int) string {
	body = strings.TrimSpace(body)
	if body == "" {
		body = "None"
	}
	content := styleSidebarLabel.Render(title) + "\n" + body
	return styleFrame.Width(max(width-4, 16)).Render(content)
}

func (m *model) renderTeamRail(team *TeamStatus, width int) string {
	var lines []string
	threadTitle := "No active thread"
	threadStatus := team.Status
	if team.ActiveThread != nil {
		threadTitle = team.ActiveThread.Title
		threadStatus = team.ActiveThread.Status
	}
	lines = append(lines, styleSidebarValue.Render(team.Name))
	lines = append(lines, styleDim.Render(fmt.Sprintf("%s · %s", threadStatus, threadTitle)))
	if team.ActiveSpeaker != "" {
		lines = append(lines, styleDim.Render("speaker · "+team.ActiveSpeaker))
	}
	if team.SummaryTime != "" {
		lines = append(lines, styleDim.Render("summary · "+team.SummaryTime))
	}
	lines = append(lines, "")
	if len(team.Members) == 0 {
		lines = append(lines, styleDim.Render("No team members"))
	} else {
		limit := min(len(team.Members), 6)
		for i := 0; i < limit; i++ {
			member := team.Members[i]
			lines = append(lines, fmt.Sprintf("%s %s", styleAgent.Render(truncate(member.Name, max(width-10, 8))), styleDim.Render(member.Role)))
		}
		if len(team.Members) > limit {
			lines = append(lines, styleDim.Render(fmt.Sprintf("+%d more", len(team.Members)-limit)))
		}
	}
	return m.renderSidebarSection("Team Rail", lines)
}

func (m *model) renderLiveSection(width int) string {
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
		lines = append(lines, styleDim.Render("No live coordination"))
	}
	for _, ts := range m.activeTools() {
		lines = append(lines, fmt.Sprintf("%s %s", ts.spinner.View(), truncate(ts.name+" "+ts.args, max(width-4, 8))))
	}
	return m.renderSidebarSection("Live Coordination", lines)
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
	lines := inputHeight + 2 + mainHeaderLines + transcriptLines + composerLines
	lines += m.teamPanelHeight(m.mainPaneWidth())

	if !m.useSidebar() && m.hasLiveSidebarContent() {
		lines += 3
		lines += min(len(m.agentKeys), 2) * 3
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
	if m.width < max(minWideWidth, minMainWidth+sidebarMinWidth+sidebarGap) {
		return false
	}
	return m.shouldShowSidebar()
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

func (m *model) shouldShowSidebar() bool {
	if m.statusInfo().Team != nil {
		return true
	}
	if len(m.agentKeys) > 0 || len(m.delegateIDs) > 0 || len(m.activeTools()) > 0 {
		return true
	}
	return len(m.statusInfo().Agents) > 1
}

func (m *model) teamPanelHeight(width int) int {
	panel := m.renderTeamPanel(width)
	if panel == "" {
		return 0
	}
	return lipgloss.Height(panel) + 1
}

func (m *model) hasLiveSidebarContent() bool {
	return len(m.agentKeys) > 0 || len(m.delegateIDs) > 0 || len(m.activeTools()) > 0
}

func (m *model) statusInfo() StatusInfo {
	if m.statusCache != nil {
		return *m.statusCache
	}
	if m.cli == nil || m.cli.statusFn == nil {
		return StatusInfo{}
	}
	status := m.cli.statusFn()
	m.statusCache = &status
	return status
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
