package cliapp

import (
	"fmt"
	"sort"
	"strings"

	"github.com/rivo/uniseg"

	"github.com/abcdlsj/mink/tool"
)

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
	headerLines := 3 // title + combined line + rule (normal mode)
	if m.statusInfo().Team != nil {
		headerLines = 4 // title + status + metadata + rule (team mode)
	}
	lines := inputHeight + 2 + headerLines + transcriptLines + composerLines

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
	if m.statusInfo().Team != nil {
		m.input.Placeholder = "Message team... (Enter: submit, Ctrl+J: newline)"
	} else {
		m.input.Placeholder = "Type message... (Enter: submit, Ctrl+J: newline)"
	}
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
	if len(m.agentKeys) > 0 || len(m.delegateIDs) > 0 || len(m.activeTools()) > 0 {
		return true
	}
	return len(m.statusInfo().Agents) > 1
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
