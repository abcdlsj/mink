package cli

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"github.com/abcdlsj/mink/textutil"
)

var shellTheme = struct {
	Base          lipgloss.Style
	NoBorder      lipgloss.Border
	Header        lipgloss.Style
	HeaderMeta    lipgloss.Style
	Title         lipgloss.Style
	Panel         lipgloss.Style
	PanelMuted    lipgloss.Style
	Composer      lipgloss.Style
	Prompt        lipgloss.Style
	Footer        lipgloss.Style
	FooterKey     lipgloss.Style
	FooterVal     lipgloss.Style
	Overlay       lipgloss.Style
	OverlayBody   lipgloss.Style
	Text          lipgloss.Style
	TextMuted     lipgloss.Style
	Meta          lipgloss.Style
	Divider       lipgloss.Style
	User          lipgloss.Style
	Assistant     lipgloss.Style
	Tool          lipgloss.Style
	Note          lipgloss.Style
	Error         lipgloss.Style
	SelectedBody  lipgloss.Style
	BadgeMuted    lipgloss.Style
	StatusRunning lipgloss.Style
	StatusDone    lipgloss.Style
	StatusFailed  lipgloss.Style
	Expanded      lipgloss.Style
}{
	Base:          lipgloss.NewStyle(),
	NoBorder:      lipgloss.HiddenBorder(),
	Header:        lipgloss.NewStyle().Border(lipgloss.RoundedBorder()),
	HeaderMeta:    lipgloss.NewStyle().Faint(true),
	Title:         lipgloss.NewStyle().Bold(true),
	Panel:         lipgloss.NewStyle(),
	PanelMuted:    lipgloss.NewStyle().Faint(true),
	Composer:      lipgloss.NewStyle(),
	Prompt:        lipgloss.NewStyle().Foreground(lipgloss.Color("6")),
	Footer:        lipgloss.NewStyle().Faint(true),
	FooterKey:     lipgloss.NewStyle().Faint(true),
	FooterVal:     lipgloss.NewStyle(),
	Overlay:       lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(1, 2),
	OverlayBody:   lipgloss.NewStyle(),
	Text:          lipgloss.NewStyle(),
	TextMuted:     lipgloss.NewStyle().Faint(true),
	Meta:          lipgloss.NewStyle().Faint(true),
	Divider:       lipgloss.NewStyle().Faint(true),
	User:          lipgloss.NewStyle().Bold(true).Faint(true),
	Assistant:     lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("5")),
	Tool:          lipgloss.NewStyle().Faint(true),
	Note:          lipgloss.NewStyle().Faint(true),
	Error:         lipgloss.NewStyle().Foreground(lipgloss.Color("1")),
	SelectedBody:  lipgloss.NewStyle(),
	BadgeMuted:    lipgloss.NewStyle().Faint(true),
	StatusRunning: lipgloss.NewStyle().Foreground(lipgloss.Color("6")),
	StatusDone:    lipgloss.NewStyle().Foreground(lipgloss.Color("2")),
	StatusFailed:  lipgloss.NewStyle().Foreground(lipgloss.Color("1")),
	Expanded:      lipgloss.NewStyle().Faint(true),
}

var shellSpin = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func (m shellModel) renderHeader() string {
	st := m.state()
	w := max(1, m.width-4)
	title := shellTheme.Title.Render(">_ Mink")
	lines := []string{
		runewidth.Truncate(title, w, "…"),
		"",
		"model:     " + shellTheme.HeaderMeta.Render(headerValue(nonEmpty(st.Model, "unknown"), w-lipgloss.Width("model:     "))),
		"directory: " + shellTheme.HeaderMeta.Render(headerValue(st.Cwd, w-lipgloss.Width("directory: "))),
	}
	return shellTheme.Header.Width(max(1, m.width-2)).Render(strings.Join(lines, "\n"))
}

func (m shellModel) renderTranscript() string {
	if m.viewport.Width <= 0 || m.viewport.Height <= 0 {
		return ""
	}
	body := m.viewport.View()
	if strings.TrimSpace(body) == "" {
		body = ""
	}
	return shellTheme.Panel.Width(m.width).Height(m.viewport.Height).Render(body)
}

func (m shellModel) renderStatus() string {
	if !m.busy {
		return ""
	}
	started := m.turn.started
	elapsed := 0
	if !started.IsZero() {
		elapsed = int(time.Since(started).Round(time.Second).Seconds())
	}
	head := shellTheme.StatusRunning.Render("• Working")
	tail := shellTheme.TextMuted.Render(fmt.Sprintf(" (%s · esc to interrupt)", formatElapsed(elapsed)))
	return shellTheme.Panel.Width(m.width).Render(head + tail)
}

func (m shellModel) renderComposer() string {
	input := m.input.View()
	return shellTheme.Composer.Width(m.width).Render(input)
}

func (m shellModel) renderFooter() string {
	st := m.state()
	if m.overlay == overlayApproval {
		return shellTheme.Footer.Width(m.width).Render("  y allow once   a allow always   n deny   esc cancel")
	}

	left := "  ? for shortcuts"
	right := "session " + shortID(st.Session) + "   cwd " + st.Cwd
	custom := m.execStatusLine()
	if custom != "" {
		right = custom
	}
	return shellTheme.Footer.Width(m.width).Render(alignFooter(left, right, m.width))
}

func (m shellModel) execStatusLine() string {
	if m.app == nil {
		return ""
	}
	script := strings.TrimSpace(m.app.Config().StatusLine)
	if script == "" {
		return ""
	}
	return execStatusScript(script)
}

func (m shellModel) renderItem(item *chatItem, idx int) []string {
	if item == nil {
		return nil
	}
	lines := m.renderItemLead(item, idx)
	for i := 0; i < len(item.Segments); {
		switch item.Segments[i].Kind {
		case segText:
			lines = append(lines, m.renderTextSegment(item, item.Segments[i], idx)...)
			i++
		case segReasoning:
			lines = append(lines, m.renderReasoningSegment(item.Segments[i], idx)...)
			i++
		case segTool:
			j := i + 1
			for j < len(item.Segments) && item.Segments[j].Kind == segTool {
				j++
			}
			lines = append(lines, m.renderToolRun(item.Segments[i:j], idx == m.selected)...)
			i = j
		default:
			i++
		}
	}
	if m.expanded == idx {
		lines = append(lines, m.renderExpanded(item)...)
	}
	return lines
}

func (m shellModel) renderToolLine(seg chatSegment, selected bool) string {
	icon := shellTheme.StatusRunning.Render("•")
	label := shellTheme.TextMuted.Render(" Running ")
	switch seg.Status {
	case "done":
		icon = shellTheme.StatusDone.Render("✓")
		label = shellTheme.TextMuted.Render(" Ran ")
	case "failed":
		icon = shellTheme.StatusFailed.Render("✗")
		label = shellTheme.Error.Render(" Failed ")
	}
	body := textutil.Preview(seg.Text, max(16, m.viewport.Width-12))
	line := icon + label + shellTheme.TextMuted.Render(body)
	if selected {
		return shellTheme.SelectedBody.Render(line)
	}
	return line
}

func (m shellModel) renderToolRun(segs []chatSegment, selected bool) []string {
	if len(segs) == 0 {
		return nil
	}
	if len(segs) == 1 {
		return []string{"", m.renderToolLine(segs[0], selected), ""}
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
	label := fmt.Sprintf(" Ran %d tools ", len(segs))
	switch {
	case failed > 0:
		icon = shellTheme.StatusFailed.Render("✗")
		label = fmt.Sprintf(" Failed %d tools ", failed)
	case running > 0:
		icon = shellTheme.StatusRunning.Render("•")
		label = fmt.Sprintf(" Running %d tools ", running)
	}
	line := icon + shellTheme.TextMuted.Render(label+textutil.Preview(strings.Join(parts, " · "), max(18, m.viewport.Width-18)))
	if selected {
		line = shellTheme.SelectedBody.Render(line)
	}
	return []string{"", line, ""}
}

func (m shellModel) approvalBody() string {
	if len(m.approvals) == 0 {
		return "No pending approvals."
	}
	req := m.approvals[0].req
	body := fmt.Sprintf("Action\n%s\n\nPattern\n%s\n\nPress y to allow once, a to allow always, n to deny.", req.Action, req.Pattern)
	return strings.Join(wrapDisplay(body, max(16, m.width-18)), "\n")
}

func (m shellModel) renderItemLead(item *chatItem, idx int) []string {
	if item.Kind == itemUser || item.Kind == itemAssistant {
		return nil
	}
	var head string
	switch item.Kind {
	case itemNotice:
		head = shellTheme.Note.Render("• Notice")
	case itemError:
		head = shellTheme.Error.Render("✗ Error")
	default:
		head = shellTheme.BadgeMuted.Render("• Item")
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
	return []string{lipgloss.JoinHorizontal(lipgloss.Left, head, " ", shellTheme.Meta.Render(meta))}
}

func (m shellModel) renderTextSegment(item *chatItem, seg chatSegment, idx int) []string {
	if item.Kind == itemUser {
		return m.renderUserText(seg.Text, idx)
	}
	lines := renderMarkdown(seg.Text, max(12, m.viewport.Width))
	if len(lines) == 0 {
		return nil
	}
	out := make([]string, 0, len(lines)+1)
	for _, line := range lines {
		if idx == m.selected {
			out = append(out, shellTheme.SelectedBody.Render(line))
			continue
		}
		out = append(out, line)
	}
	out = append(out, "")
	return out
}

func (m shellModel) renderReasoningSegment(seg chatSegment, idx int) []string {
	text := strings.TrimSpace(textutil.Valid(seg.Text))
	if text == "" {
		return nil
	}
	body := textutil.Preview(strings.Join(strings.Fields(text), " "), max(16, m.viewport.Width-13))
	line := shellTheme.Tool.Render("✦ Thinking ") + shellTheme.TextMuted.Render(body)
	if idx == m.selected {
		line = shellTheme.SelectedBody.Render(line)
	}
	return []string{"", line, ""}
}

func (m shellModel) renderUserText(text string, idx int) []string {
	lines := wrapDisplay(strings.TrimRight(textutil.Valid(text), "\r\n"), max(12, m.viewport.Width-2))
	out := make([]string, 0, len(lines)+2)
	out = append(out, "")
	for i, line := range lines {
		prefix := "  "
		if i == 0 {
			prefix = shellTheme.User.Render("› ")
		}
		row := prefix + line
		if idx == m.selected {
			row = shellTheme.SelectedBody.Render(row)
		}
		out = append(out, row)
	}
	out = append(out, "")
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
			out = append(out, indentLines(renderMarkdown(seg.Detail, max(16, m.viewport.Width-6)), "    ")...)
		}
	}
	if len(out) == 0 {
		out = indentLines(renderMarkdown(item.detailText(), max(16, m.viewport.Width-6)), "  └ ")
	}
	for i, line := range out {
		out[i] = shellTheme.Expanded.Render(line)
	}
	return out
}

func (m shellModel) renderOverlay(base, title, body string) string {
	w := min(max(52, m.width*2/3), m.width-4)
	h := min(max(10, m.height*2/3), m.height-4)
	panel := shellTheme.Overlay.Width(w).Height(h).Render(
		shellTheme.Title.Render(title) + "\n\n" + shellTheme.OverlayBody.Render(body),
	)
	x := max(0, (m.width-lipgloss.Width(panel))/2)
	y := max(0, (m.height-lipgloss.Height(panel))/2)
	return placeOverlay(base, panel, x, y)
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
		left := runewidth.Truncate(lines[row], x, "")
		right := ""
		if w := lipgloss.Width(lines[row]); w > x+lipgloss.Width(line) {
			right = runewidth.TruncateLeft(lines[row], w-(x+lipgloss.Width(line)), "")
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

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
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

func headerValue(s string, width int) string {
	return runewidth.Truncate(textutil.Valid(s), max(1, width), "…")
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
		right = runewidth.Truncate(right, room, "…")
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
