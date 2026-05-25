package cli

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"

	"github.com/abcdlsj/sumi/textutil"
	"github.com/abcdlsj/sumi/tool"
)

var shellTheme = struct {
	Base          lipgloss.Style
	NoBorder      lipgloss.Border
	Header        lipgloss.Style
	HeaderMeta    lipgloss.Style
	Title         lipgloss.Style
	Chip          lipgloss.Style
	ChipDim       lipgloss.Style
	Panel         lipgloss.Style
	Composer      lipgloss.Style
	Prompt        lipgloss.Style
	Footer        lipgloss.Style
	Overlay       lipgloss.Style
	OverlayBody   lipgloss.Style
	Suggest       lipgloss.Style
	SuggestActive lipgloss.Style
	Text          lipgloss.Style
	TextMuted     lipgloss.Style
	Meta          lipgloss.Style
	User          lipgloss.Style
	Tool          lipgloss.Style
	Note          lipgloss.Style
	Error         lipgloss.Style
	SelectedBody  lipgloss.Style
	BadgeMuted    lipgloss.Style
	StatusRunning lipgloss.Style
	StatusDone    lipgloss.Style
	StatusFailed  lipgloss.Style
	Expanded      lipgloss.Style
	ApprovalBox   lipgloss.Style
}{
	Base:          lipgloss.NewStyle(),
	NoBorder:      lipgloss.HiddenBorder(),
	Header:        lipgloss.NewStyle(),
	HeaderMeta:    lipgloss.NewStyle().Faint(true),
	Title:         lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6")),
	Chip:          lipgloss.NewStyle().Foreground(lipgloss.Color("6")),
	ChipDim:       lipgloss.NewStyle().Faint(true),
	Panel:         lipgloss.NewStyle(),
	Composer:      lipgloss.NewStyle(),
	Prompt:        lipgloss.NewStyle().Foreground(lipgloss.Color("6")),
	Footer:        lipgloss.NewStyle().Faint(true),
	Overlay:       lipgloss.NewStyle().Padding(1, 2).Background(lipgloss.Color("235")),
	OverlayBody:   lipgloss.NewStyle(),
	Suggest:       lipgloss.NewStyle().Faint(true),
	SuggestActive: lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Background(lipgloss.Color("235")),
	Text:          lipgloss.NewStyle(),
	TextMuted:     lipgloss.NewStyle().Faint(true),
	Meta:          lipgloss.NewStyle().Faint(true),
	User:          lipgloss.NewStyle().Bold(true).Faint(true),
	Tool:          lipgloss.NewStyle().Faint(true),
	Note:          lipgloss.NewStyle().Faint(true),
	Error:         lipgloss.NewStyle().Foreground(lipgloss.Color("1")),
	SelectedBody:  lipgloss.NewStyle(),
	BadgeMuted:    lipgloss.NewStyle().Faint(true),
	StatusRunning: lipgloss.NewStyle().Foreground(lipgloss.Color("6")),
	StatusDone:    lipgloss.NewStyle().Foreground(lipgloss.Color("2")),
	StatusFailed:  lipgloss.NewStyle().Foreground(lipgloss.Color("1")),
	Expanded:      lipgloss.NewStyle().Faint(true),
	ApprovalBox:   lipgloss.NewStyle().Border(lipgloss.NormalBorder(), true, false, true, false).BorderForeground(lipgloss.Color("8")).Padding(0, 1),
}

var shellSpin = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const shellContentMaxWidth = 100
const shellContentIndent = 2

func (m shellModel) contentWidth() int {
	w := m.viewport.Width
	if w <= 0 {
		w = m.width
	}
	w -= shellContentIndent
	if w < 1 {
		w = 1
	}
	if w > shellContentMaxWidth {
		return shellContentMaxWidth
	}
	return w
}

func (m shellModel) renderHeader(st cliState) string {
	w := max(1, m.width)
	lines := []string{
		padLine(headerLine(st, w-4), w),
		"",
	}
	return shellTheme.Header.Width(w).Render(strings.Join(lines, "\n"))
}

func (m shellModel) renderTranscript() string {
	if m.viewport.Width <= 0 || m.viewport.Height <= 0 {
		return ""
	}
	body := m.viewport.View()
	if strings.TrimSpace(body) == "" {
		body = ""
	} else {
		body = indentBlock(body, strings.Repeat(" ", shellContentIndent))
	}
	return shellTheme.Panel.Width(m.width).Height(m.viewport.Height).Render(body)
}

func (m shellModel) renderStatus() string {
	if len(m.approvals) > 0 {
		return m.renderApprovalBox()
	}
	if !m.busy {
		return shellTheme.Panel.Width(m.width).Render(padLine("", m.width))
	}
	started := m.turn.started
	elapsed := 0
	if !started.IsZero() {
		elapsed = int(time.Since(started).Round(time.Second).Seconds())
	}
	spin := shellSpin[m.spinner%len(shellSpin)]
	head := shellTheme.StatusRunning.Render(spin + " working")
	parts := []string{formatElapsed(elapsed)}
	if len(m.queue) > 0 {
		parts = append(parts, fmt.Sprintf("%d queued", len(m.queue)))
	}
	parts = append(parts, "esc interrupt")
	tail := shellTheme.TextMuted.Render("  " + strings.Join(parts, " · "))
	return shellTheme.Panel.Width(m.width).Render(padLine(head+tail, m.width))
}

type approvalOption struct {
	label string
	value tool.Approval
}

func approvalOptions() []approvalOption {
	return []approvalOption{
		{label: "Yes", value: tool.AllowOnce},
		{label: "Yes, don't ask again for this pattern", value: tool.AllowAlways},
		{label: "No, tell Sumi what to do differently", value: tool.Denied},
	}
}

func (m shellModel) renderApprovalBox() string {
	req := m.approvals[0].req
	name := strings.TrimSpace(req.Tool)
	if name == "" {
		name = "tool"
	}
	action := strings.TrimSpace(req.Action)
	if action == "" {
		action = name
	}
	width := max(20, m.width)
	inner := max(8, width-4)

	title := shellTheme.Title.Render(name)
	if n := len(m.approvals) - 1; n > 0 {
		title += shellTheme.TextMuted.Render(fmt.Sprintf("  (+%d queued)", n))
	}
	lines := []string{title}
	for _, row := range wrapDisplay(action, inner) {
		lines = append(lines, shellTheme.TextMuted.Render(row))
	}
	if p := strings.TrimSpace(req.Pattern); p != "" {
		lines = append(lines, shellTheme.TextMuted.Render("pattern  "+textutil.Preview(p, max(8, inner-9))))
	}
	lines = append(lines, "")
	for i, opt := range approvalOptions() {
		prefix := "  "
		style := shellTheme.Text
		if i == m.approvalPick {
			prefix = "› "
			style = shellTheme.Chip
		}
		lines = append(lines, style.Render(prefix+opt.label))
	}

	return shellTheme.ApprovalBox.Width(width).Render(strings.Join(lines, "\n"))
}

func (m shellModel) renderComposer() string {
	parts := m.renderSuggestions()
	if len(parts) > 0 {
		parts = append(parts, "")
	}
	parts = append(parts, "")
	parts = append(parts, strings.TrimRight(m.input.View(), "\n"))
	return shellTheme.Composer.Width(m.width).Render(strings.Join(parts, "\n"))
}

func (m shellModel) renderFooter(st cliState) string {
	if len(m.approvals) > 0 {
		return shellTheme.Footer.Width(m.width).Render(padLine("enter confirm   esc deny", m.width))
	}
	if m.overlay == overlaySession {
		return shellTheme.Footer.Width(m.width).Render(padLine("type filter   enter switch   n new   j/k move   esc close", m.width))
	}

	left := "/ commands   /channel   /thread   tab focus"
	right := "session " + shortID(st.Session) + "   " + st.Cwd
	if len(m.queue) > 0 {
		right = fmt.Sprintf("%d queued   %s", len(m.queue), right)
	}
	custom := m.execStatusLine()
	if custom != "" {
		right = custom
	}
	return shellTheme.Footer.Width(m.width).Render(padLine(alignFooter(left, right, max(1, m.width-4)), m.width))
}

func (m shellModel) renderSuggestions() []string {
	if len(m.suggests) == 0 || m.suggestRows == 0 {
		return nil
	}
	limit := min(len(m.suggests), m.suggestRows)
	start := 0
	if m.suggest >= limit {
		start = m.suggest - limit + 1
	}
	if start+limit > len(m.suggests) {
		start = len(m.suggests) - limit
	}
	lines := make([]string, 0, limit)
	width := max(20, m.width)
	for i := 0; i < limit; i++ {
		idx := start + i
		item := m.suggests[idx]
		prefix := "  "
		style := shellTheme.Suggest
		if idx == m.suggest {
			prefix = "› "
			style = shellTheme.SuggestActive
		}
		label, desc := completionLabel(item)
		name := style.Render(prefix + runewidth.Truncate(label, max(8, width-24), "…"))
		desc = shellTheme.TextMuted.Render(desc)
		lines = append(lines, style.Width(width).Render(alignFooter(name, desc, width)))
	}
	return lines
}

func completionLabel(h completionHint) (string, string) {
	switch h.Kind {
	case completionCommand:
		return "/" + h.Value, h.Desc
	case completionMention:
		return "@" + h.Value, h.Desc
	case completionFile:
		return "@" + h.Value, "file"
	case completionPersona:
		return "@" + h.Value, h.Desc
	default:
		return h.Value, h.Desc
	}
}

func (m shellModel) execStatusLine() string {
	return m.statusLine
}

func (m shellModel) renderItem(item *chatItem, idx int) []string {
	if item == nil {
		return nil
	}
	if item.Kind == itemAssistant {
		return m.renderAssistantItem(item, idx)
	}
	lines := m.renderItemLead(item, idx)
	hasBody := false
	add := func(seg []string) {
		if len(seg) == 0 {
			return
		}
		if hasBody {
			lines = append(lines, "")
		}
		lines = append(lines, seg...)
		hasBody = true
	}
	for i := 0; i < len(item.Segments); {
		switch item.Segments[i].Kind {
		case segText:
			add(m.renderTextSegment(item, item.Segments[i], idx))
			i++
		case segReasoning:
			add(m.renderReasoningSegment(item.Segments[i].Text, idx))
			i++
		case segTool:
			j := i + 1
			for j < len(item.Segments) && item.Segments[j].Kind == segTool {
				j++
			}
			add(m.renderToolRun(item.Segments[i:j], idx == m.selected))
			i = j
		default:
			i++
		}
	}
	if m.expanded == idx {
		add(m.renderExpanded(item))
	}
	return lines
}

func (m shellModel) renderAssistantItem(item *chatItem, idx int) []string {
	var lines []string
	hasBody := false
	add := func(seg []string) {
		if len(seg) == 0 {
			return
		}
		if hasBody {
			lines = append(lines, "")
		}
		lines = append(lines, seg...)
		hasBody = true
	}
	if m.showMessageIDs() && item.ID != "" {
		meta := shellTheme.Meta.Render("sumi " + item.ID)
		if idx == m.selected {
			meta = m.selectedLine(meta)
		}
		lines = append(lines, meta)
	}
	if text := reasoningText(item); text != "" {
		add(m.renderReasoningSegment(text, idx))
	}
	if text := itemText(item); text != "" {
		add(m.renderTextSegment(item, chatSegment{Kind: segText, Text: text}, idx))
	}
	tools := visibleTools(item)
	if len(tools) > 0 {
		add(m.renderToolRun(tools, idx == m.selected))
	}
	if m.expanded == idx {
		add(m.renderExpanded(item))
	}
	return lines
}

func itemText(item *chatItem) string {
	if item == nil {
		return ""
	}
	var b strings.Builder
	for _, seg := range item.Segments {
		if seg.Kind == segText {
			b.WriteString(seg.Text)
		}
	}
	return textutil.Valid(b.String())
}

func visibleTools(item *chatItem) []chatSegment {
	if item == nil {
		return nil
	}
	hasText := strings.TrimSpace(itemText(item)) != ""
	out := make([]chatSegment, 0, len(item.Segments))
	for _, seg := range item.Segments {
		if seg.Kind != segTool {
			continue
		}
		if hasText && seg.Status != "running" {
			continue
		}
		out = append(out, seg)
	}
	return out
}

func (m shellModel) renderToolLine(seg chatSegment, selected bool) string {
	name := strings.TrimSpace(seg.Tool)
	if name == "" {
		name = "tool"
	}
	icon := shellTheme.StatusRunning.Render("●")
	label := shellTheme.TextMuted.Render(" running ")
	switch seg.Status {
	case "done":
		icon = shellTheme.StatusDone.Render("✓")
		label = shellTheme.TextMuted.Render(" ran ")
	case "failed":
		icon = shellTheme.StatusFailed.Render("✗")
		label = shellTheme.Error.Render(" failed ")
	}
	head := icon + label + shellTheme.Chip.Render(name)
	width := max(20, m.contentWidth())
	room := max(16, width-lipgloss.Width(head)-4)
	body := textutil.Preview(seg.Text, room)
	line := head + shellTheme.TextMuted.Render("  "+body)
	if selected {
		return m.selectedLine(line)
	}
	return line
}

func (m shellModel) renderToolRun(segs []chatSegment, selected bool) []string {
	if len(segs) == 0 {
		return nil
	}
	if len(segs) == 1 {
		return []string{m.renderToolLine(segs[0], selected)}
	}
	var failed, running int
	counts := map[string]int{}
	var order []string
	for _, seg := range segs {
		switch seg.Status {
		case "done":
		case "failed":
			failed++
		default:
			running++
		}
		name := strings.TrimSpace(seg.Tool)
		if name == "" {
			name = "tool"
		}
		if counts[name] == 0 {
			order = append(order, name)
		}
		counts[name]++
	}
	parts := []string{fmt.Sprintf("%d calls", len(segs))}
	for _, name := range order {
		parts = append(parts, fmt.Sprintf("%s %d", name, counts[name]))
	}
	icon := shellTheme.StatusDone.Render("✓")
	label := fmt.Sprintf(" ran %d tools ", len(segs))
	switch {
	case failed > 0:
		icon = shellTheme.StatusFailed.Render("✗")
		label = fmt.Sprintf(" failed %d tools ", failed)
	case running > 0:
		icon = shellTheme.StatusRunning.Render("●")
		label = fmt.Sprintf(" running %d tools ", running)
	}
	headText := icon + shellTheme.TextMuted.Render(label)
	room := max(18, m.contentWidth()-lipgloss.Width(headText)-4)
	line := headText + shellTheme.TextMuted.Render(textutil.Preview(strings.Join(parts, " · "), room))
	if selected {
		line = m.selectedLine(line)
	}
	return []string{line}
}

func (m shellModel) renderSessionOverlay(base string) string {
	empty := "No sessions."
	if strings.TrimSpace(m.sessionQuery) != "" {
		empty = "No matching sessions."
	}
	return m.renderPopupList(base, popupList{
		Title:    "Sessions",
		Hint:     "type filter · enter select · esc close",
		Query:    m.sessionQuery,
		Empty:    empty,
		Items:    m.sessionItems(),
		Selected: m.session,
	})
}

func (m shellModel) renderItemLead(item *chatItem, idx int) []string {
	if item.Kind == itemUser || item.Kind == itemAssistant {
		return nil
	}
	var head string
	switch item.Kind {
	case itemNotice:
		head = shellTheme.Note.Render("notice")
	case itemError:
		head = shellTheme.Error.Render("error")
	default:
		head = shellTheme.BadgeMuted.Render("item")
	}
	meta := item.Time.Format("15:04:05")
	if item.Status != "" {
		meta += " · " + item.Status
	}
	if m.expanded == idx {
		meta += " · expanded"
	}
	if idx == m.selected {
		meta = "› " + meta
	}
	line := lipgloss.JoinHorizontal(lipgloss.Left, head, "  ", shellTheme.Meta.Render(meta))
	if idx == m.selected {
		line = m.selectedLine(line)
	}
	return []string{line}
}

func (m shellModel) renderTextSegment(item *chatItem, seg chatSegment, idx int) []string {
	if item.Kind == itemUser {
		return m.renderUserText(seg.Text, idx)
	}
	lines := renderMarkdown(seg.Text, max(12, m.contentWidth()))
	if len(lines) == 0 {
		return nil
	}
	out := make([]string, 0, len(lines)+1)
	for _, line := range lines {
		if idx == m.selected {
			out = append(out, m.selectedLine(line))
			continue
		}
		out = append(out, line)
	}
	return out
}

func reasoningText(item *chatItem) string {
	if item == nil {
		return ""
	}
	var parts []string
	for _, seg := range item.Segments {
		if seg.Kind != segReasoning {
			continue
		}
		if text := strings.TrimSpace(textutil.Valid(seg.Text)); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n")
}

func (m shellModel) renderReasoningSegment(text string, idx int) []string {
	text = strings.TrimSpace(textutil.Valid(text))
	if text == "" {
		return nil
	}
	width := max(16, m.contentWidth())
	head := shellTheme.Tool.Render("✦ Thinking")
	lines := wrapDisplay(text, max(8, width-2))
	out := make([]string, 0, len(lines)+1)
	out = append(out, head)
	for _, line := range lines {
		row := shellTheme.TextMuted.Render("  " + line)
		if idx == m.selected {
			row = m.selectedLine(row)
		}
		out = append(out, row)
	}
	if idx == m.selected {
		out[0] = m.selectedLine(head)
	}
	return out
}

func (m shellModel) renderUserText(text string, idx int) []string {
	lines := wrapDisplay(strings.TrimRight(textutil.Valid(text), "\r\n"), max(12, m.contentWidth()-2))
	out := make([]string, 0, len(lines))
	for i, line := range lines {
		prefix := "  "
		if i == 0 {
			id := ""
			if m.showMessageIDs() && idx >= 0 && idx < len(m.items) && m.items[idx] != nil && m.items[idx].ID != "" {
				id = m.items[idx].ID + " "
			}
			prefix = shellTheme.User.Render("› " + id)
		}
		row := prefix + line
		if idx == m.selected {
			row = m.selectedLine(row)
		}
		out = append(out, row)
	}
	return out
}

func (m shellModel) renderExpanded(item *chatItem) []string {
	var out []string
	for _, seg := range item.Segments {
		switch seg.Kind {
		case segText:
			continue
		case segTool:
			title := shellTheme.Tool.Render("  └ tool details")
			out = append(out, title)
			out = append(out, indentLines(renderMarkdown(seg.Detail, max(16, m.contentWidth()-6)), "    ")...)
		}
	}
	if len(out) == 0 {
		out = indentLines(renderMarkdown(item.detailText(), max(16, m.contentWidth()-6)), "  └ ")
	}
	for i, line := range out {
		out[i] = shellTheme.Expanded.Render(line)
	}
	return out
}

func (m shellModel) selectedLine(line string) string {
	return shellTheme.SelectedBody.Render(line)
}

func (m shellModel) showMessageIDs() bool {
	return m.thread != nil
}

func wrapDisplay(s string, width int) []string {
	s = strings.ReplaceAll(textutil.Valid(s), "\r\n", "\n")
	if width <= 0 {
		return []string{""}
	}
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if line == "" {
			out = append(out, "")
			continue
		}
		var b strings.Builder
		w := 0
		for _, r := range []rune(line) {
			rw := runewidth.RuneWidth(r)
			if rw <= 0 {
				rw = 1
			}
			if w+rw > width {
				out = append(out, b.String())
				b.Reset()
				w = 0
			}
			b.WriteRune(r)
			w += rw
		}
		out = append(out, b.String())
	}
	if len(out) == 0 {
		return []string{""}
	}
	return out
}

func placeOverlay(base, overlay string, x, y int) string {
	lines := strings.Split(base, "\n")
	box := strings.Split(overlay, "\n")
	for len(lines) < y+len(box) {
		lines = append(lines, "")
	}
	for i, line := range box {
		row := y + i
		for lipgloss.Width(lines[row]) < x {
			lines[row] += " "
		}
		left := ansi.Truncate(lines[row], x, "")
		right := ""
		if w := lipgloss.Width(lines[row]); w > x+lipgloss.Width(line) {
			right = ansi.TruncateLeft(lines[row], w-(x+lipgloss.Width(line)), "")
		}
		lines[row] = left + line + right
	}
	return strings.Join(lines, "\n")
}

func lipJoinVertical(parts ...string) string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return lipgloss.JoinVertical(lipgloss.Left, out...)
}

func viewHeight(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(strings.TrimRight(s, "\n"), "\n") + 1
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func headerLine(st cliState, width int) string {
	width = max(1, width)
	session := shellTheme.Chip.Render(nonEmpty(st.Session, "(new)"))
	if lipgloss.Width(session) >= width {
		return ansi.Truncate(session, width, "…")
	}
	left := strings.Join([]string{
		shellTheme.Title.Render("Sumi"),
		shellTheme.Chip.Render(spaceLabel(st)),
		shellTheme.ChipDim.Render("model ") + shellTheme.Text.Render(nonEmpty(st.Model, "unknown")),
		shellTheme.ChipDim.Render("cwd ") + shellTheme.Text.Render(nonEmpty(st.Cwd, ".")),
	}, "  ")
	room := width - lipgloss.Width(session) - 2
	if lipgloss.Width(left) > room {
		left = ansi.Truncate(left, max(1, room), "…")
	}
	return alignFooter(left, session, width)
}

func spaceLabel(st cliState) string {
	ch := strings.TrimSpace(st.Channel)
	if ch == "" {
		ch = "#main"
	}
	if th := strings.TrimSpace(st.Thread); th != "" {
		return ch + " > " + th
	}
	return ch
}

func padLine(s string, width int) string {
	width = max(1, width)
	s = textutil.Valid(s)
	if width <= 2 {
		return ansi.Truncate(s, width, "")
	}
	if lipgloss.Width(s) > width-2 {
		s = ansi.Truncate(s, max(1, width-2), "…")
	}
	return "  " + s + strings.Repeat(" ", max(0, width-2-lipgloss.Width(s)))
}

func nonEmpty(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

func shortID(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "(new)"
	}
	if len(s) > 8 && s != "(new)" {
		return s[:8]
	}
	return s
}

func alignFooter(left, right string, width int) string {
	if width <= 0 {
		return ""
	}
	left = textutil.Valid(left)
	right = textutil.Valid(right)
	lw := lipgloss.Width(left)
	rw := lipgloss.Width(right)
	if lw+rw+1 > width {
		room := max(0, width-lw-1)
		right = ansi.Truncate(right, room, "…")
		rw = lipgloss.Width(right)
	}
	gap := max(1, width-lw-rw)
	return left + strings.Repeat(" ", gap) + right
}

func formatElapsed(sec int) string {
	if sec < 0 {
		sec = 0
	}
	if sec < 60 {
		return fmt.Sprintf("%ds", sec)
	}
	if sec < 3600 {
		return fmt.Sprintf("%dm %02ds", sec/60, sec%60)
	}
	return fmt.Sprintf("%dh %02dm %02ds", sec/3600, sec%3600/60, sec%60)
}

func execStatusScript(script string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-c", script)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(textutil.Valid(string(out)))
}
