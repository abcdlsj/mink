package cliapp

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

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

	var transcript strings.Builder
	m.writeOutputSection(&transcript, layout.outputHeight)

	var input strings.Builder
	m.writeInputSection(&input, confirming)

	var b strings.Builder
	b.WriteString(strings.TrimRight(header.String(), "\n"))
	b.WriteString("\n")
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
	metaBar := m.renderMetadataBar(width)

	if status.Team != nil {
		var parts []string
		parts = append(parts, status.Team.Name)
		if status.Team.ActiveThread != nil && status.Team.ActiveThread.Title != "" {
			parts = append(parts, status.Team.ActiveThread.Title)
		}
		parts = append(parts, status.Team.Status)
		b.WriteString(styleMutedBlock.Render(truncate(strings.Join(parts, "  /  "), max(width-2, 20))))
		b.WriteString("\n")
		b.WriteString(metaBar)
		b.WriteString("\n")
	} else {
		var parts []string
		if status.Model != "" {
			parts = append(parts, status.Model)
		}
		parts = append(parts, m.turnLabel())
		line := styleMutedBlock.Render(strings.Join(parts, "  /  "))
		if metaBar != "" {
			line += "  " + styleDim.Render("·") + "  " + metaBar
		}
		b.WriteString(line)
		b.WriteString("\n")
	}

	barWidth := max(width-2, 12)
	b.WriteString(styleBar.Render(strings.Repeat("─", barWidth)))
	b.WriteString("\n")
}

func (m *model) renderMetadataBar(width int) string {
	status := m.statusInfo()
	var chips []string
	chipMax := max(width/6, 12)

	if status.Team != nil {
		team := status.Team
		chips = append(chips, m.renderChip("members", fmt.Sprintf("%d", len(team.Members)), styleChipMembers))
		if team.ActiveThread != nil {
			chips = append(chips, m.renderChip("thread", truncate(team.ActiveThread.Title, chipMax), styleChipThread))
		} else {
			chips = append(chips, m.renderChip("thread", "none", styleChipThread))
		}
		blocker := "none"
		if team.CurrentBlocker != "" {
			blocker = truncate(team.CurrentBlocker, chipMax)
		}
		chips = append(chips, m.renderChip("blocker", blocker, styleChipBlocker))
		speaker := "idle"
		if team.ActiveSpeaker != "" {
			speaker = truncate(team.ActiveSpeaker, chipMax)
		}
		chips = append(chips, m.renderChip("speaker", speaker, styleChipSpeaker))
		summary := "—"
		if team.SummaryTime != "" {
			summary = team.SummaryTime
		}
		chips = append(chips, m.renderChip("summary", summary, styleChipSummary))
	} else {
		if status.Session != "" {
			chips = append(chips, m.renderChip("session", truncate(status.Session, chipMax), styleChipValue))
		}
		chips = append(chips, m.renderChip("tokens", fmt.Sprintf("in:%s out:%s", fmtTokens(status.TokenIn), fmtTokens(status.TokenOut)), styleChipValue))
	}

	return strings.Join(chips, " ")
}

func (m *model) renderChip(label, value string, valueStyle lipgloss.Style) string {
	return styleChipLabel.Render(label+":") + " " + valueStyle.Render(value)
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
	} else if m.statusInfo().Team != nil {
		b.WriteString(stylePrompt.Render("> Message team... "))
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
