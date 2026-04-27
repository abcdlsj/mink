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
	Base:          lipgloss.NewStyle().Foreground(lipgloss.Color("#E6EDF3")).Background(lipgloss.Color("#0D1117")),
	NoBorder:      lipgloss.HiddenBorder(),
	Header:        lipgloss.NewStyle().Padding(0, 2),
	HeaderMeta:    lipgloss.NewStyle().Foreground(lipgloss.Color("#8B949E")),
	Title:         lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#58A6FF")),
	Panel:         lipgloss.NewStyle().Padding(0, 2),
	PanelMuted:    lipgloss.NewStyle().Padding(0, 2).Foreground(lipgloss.Color("#8B949E")),
	Composer:      lipgloss.NewStyle().Padding(1, 2).BorderTop(true).BorderStyle(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("#21262D")),
	Footer:        lipgloss.NewStyle().Padding(0, 2).Background(lipgloss.Color("#161B22")).Foreground(lipgloss.Color("#8B949E")),
	FooterKey:     lipgloss.NewStyle().Foreground(lipgloss.Color("#7D8590")),
	FooterVal:     lipgloss.NewStyle().Foreground(lipgloss.Color("#C9D1D9")),
	Overlay:       lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#58A6FF")).Background(lipgloss.Color("#161B22")).Padding(1, 2),
	OverlayBody:   lipgloss.NewStyle().Foreground(lipgloss.Color("#C9D1D9")),
	Text:          lipgloss.NewStyle().Foreground(lipgloss.Color("#C9D1D9")),
	TextMuted:     lipgloss.NewStyle().Foreground(lipgloss.Color("#8B949E")),
	Meta:          lipgloss.NewStyle().Foreground(lipgloss.Color("#6E7681")),
	Divider:       lipgloss.NewStyle().Foreground(lipgloss.Color("#21262D")),
	User:          lipgloss.NewStyle().Foreground(lipgloss.Color("#0D1117")).Background(lipgloss.Color("#3FB950")).Bold(true).Padding(0, 1),
	Assistant:     lipgloss.NewStyle().Foreground(lipgloss.Color("#0D1117")).Background(lipgloss.Color("#58A6FF")).Bold(true).Padding(0, 1),
	Tool:          lipgloss.NewStyle().Foreground(lipgloss.Color("#0D1117")).Background(lipgloss.Color("#D29922")).Bold(true).Padding(0, 1),
	Note:          lipgloss.NewStyle().Foreground(lipgloss.Color("#C9D1D9")).Background(lipgloss.Color("#6E7681")).Bold(true).Padding(0, 1),
	Error:         lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF")).Background(lipgloss.Color("#F85149")).Bold(true).Padding(0, 1),
	SelectedBody:  lipgloss.NewStyle().Foreground(lipgloss.Color("#E6EDF3")),
	BadgeMuted:    lipgloss.NewStyle().Foreground(lipgloss.Color("#C9D1D9")).Background(lipgloss.Color("#21262D")).Padding(0, 1),
	StatusRunning: lipgloss.NewStyle().Foreground(lipgloss.Color("#0D1117")).Background(lipgloss.Color("#58A6FF")).Padding(0, 1),
	StatusDone:    lipgloss.NewStyle().Foreground(lipgloss.Color("#0D1117")).Background(lipgloss.Color("#3FB950")).Padding(0, 1),
	StatusFailed:  lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF")).Background(lipgloss.Color("#F85149")).Padding(0, 1),
	Expanded:      lipgloss.NewStyle().BorderLeft(true).BorderStyle(lipgloss.ThickBorder()).BorderForeground(lipgloss.Color("#58A6FF")).PaddingLeft(1).Foreground(lipgloss.Color("#C9D1D9")),
}

var shellSpin = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func (m shellModel) renderHeader() string {
	state := ""
	if m.busy {
		state = " " + shellSpin[m.spinner%len(shellSpin)]
	}
	title := shellTheme.Title.Render("mink") + state
	meta := shellTheme.HeaderMeta.Render(m.state().Model)
	return shellTheme.Header.Width(m.width).Render(
		lipgloss.JoinHorizontal(lipgloss.Left, title, "  ", meta),
	)
}

func (m shellModel) renderTranscript() string {
	if m.viewport.Width <= 0 || m.viewport.Height <= 0 {
		return ""
	}
	body := m.viewport.View()
	if strings.TrimSpace(body) == "" {
		body = shellTheme.PanelMuted.Render("No messages yet.\n\nStart with a task, a question, or a local command like !help.")
	}
	return shellTheme.Panel.Width(m.width).Height(m.viewport.Height).Render(body)
}

func (m shellModel) renderComposer() string {
	input := m.input.View()
	return shellTheme.Composer.Width(m.width).Render(input)
}

func (m shellModel) renderFooter() string {
	st := m.state()
	if m.overlay == overlayApproval {
		return shellTheme.Footer.Width(m.width).Render("y allow once · a allow always · n deny · esc cancel")
	}

	custom := m.execStatusLine()
	if custom != "" {
		left := lipgloss.JoinHorizontal(lipgloss.Left,
			shellTheme.FooterKey.Render("session "),
			shellTheme.FooterVal.Render(st.Session),
			"  ",
			shellTheme.FooterKey.Render("cwd "),
			shellTheme.FooterVal.Render(st.Cwd),
		)
		return shellTheme.Footer.Width(m.width).Render(
			lipgloss.JoinHorizontal(lipgloss.Left, left, "  ", shellTheme.FooterKey.Render("│"), "  ", custom),
		)
	}

	return shellTheme.Footer.Width(m.width).Render(
		lipgloss.JoinHorizontal(lipgloss.Left,
			shellTheme.FooterKey.Render("session "),
			shellTheme.FooterVal.Render(st.Session),
			"  ",
			shellTheme.FooterKey.Render("cwd "),
			shellTheme.FooterVal.Render(st.Cwd),
		),
	)
}

func (m shellModel) execStatusLine() string {
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
	head := m.renderItemHeader(item, idx)
	lines := []string{head}
	for i := 0; i < len(item.Segments); {
		switch item.Segments[i].Kind {
		case segText:
			if i == 0 {
				lines = append(lines, "")
			}
			lines = append(lines, m.renderTextSegment(item.Segments[i], idx)...)
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
	status := shellTheme.StatusRunning.Render("running")
	switch seg.Status {
	case "done":
		status = shellTheme.StatusDone.Render("done")
	case "failed":
		status = shellTheme.StatusFailed.Render("failed")
	}
	body := textutil.Preview(seg.Text, max(16, m.viewport.Width-26))
	line := "  " + shellTheme.Tool.Render("tool") + " " + shellTheme.TextMuted.Render(body) + " " + status
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
	var done, failed, running int
	counts := map[string]int{}
	var order []string
	for _, seg := range segs {
		switch seg.Status {
		case "done":
			done++
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
	status := shellTheme.StatusDone.Render("done")
	switch {
	case failed > 0:
		status = shellTheme.StatusFailed.Render(fmt.Sprintf("%d failed", failed))
	case running > 0:
		status = shellTheme.StatusRunning.Render(fmt.Sprintf("%d running", running))
	default:
		status = shellTheme.StatusDone.Render(fmt.Sprintf("%d done", done))
	}
	line := "  " + shellTheme.Tool.Render("tools") + " " + shellTheme.TextMuted.Render(textutil.Preview(strings.Join(parts, " · "), max(18, m.viewport.Width-28))) + " " + status
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

func (m shellModel) renderItemHeader(item *chatItem, idx int) string {
	var badge string
	switch item.Kind {
	case itemUser:
		badge = shellTheme.User.Render("you")
	case itemAssistant:
		badge = shellTheme.Assistant.Render("mink")
	case itemNotice:
		badge = shellTheme.Note.Render("note")
	case itemError:
		badge = shellTheme.Error.Render("error")
	default:
		badge = shellTheme.BadgeMuted.Render("item")
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
	return lipgloss.JoinHorizontal(lipgloss.Left, badge, " ", shellTheme.Meta.Render(meta))
}

func (m shellModel) renderTextSegment(seg chatSegment, idx int) []string {
	lines := renderMarkdown(seg.Text, max(12, m.viewport.Width-4))
	if len(lines) == 0 {
		return nil
	}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if idx == m.selected {
			out = append(out, shellTheme.SelectedBody.Render("  "+line))
			continue
		}
		out = append(out, "  "+line)
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
			title := shellTheme.Tool.Render("tool details")
			out = append(out, "  "+title)
			out = append(out, indentLines(renderMarkdown(seg.Detail, max(16, m.viewport.Width-10)), "  ")...)
		}
	}
	if len(out) == 0 {
		out = indentLines(renderMarkdown(item.detailText(), max(16, m.viewport.Width-10)), "  ")
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
