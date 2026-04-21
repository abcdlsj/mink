package app

import (
	"fmt"
	"strings"

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
	Overlay       lipgloss.Style
	OverlayBody   lipgloss.Style
	Text          lipgloss.Style
	TextMuted     lipgloss.Style
	User          lipgloss.Style
	Assistant     lipgloss.Style
	Tool          lipgloss.Style
	Note          lipgloss.Style
	Error         lipgloss.Style
	Selected      lipgloss.Style
	SelectedBody  lipgloss.Style
	BadgeMuted    lipgloss.Style
	StatusRunning lipgloss.Style
	StatusDone    lipgloss.Style
	StatusFailed  lipgloss.Style
}{
	Base:          lipgloss.NewStyle().Foreground(lipgloss.Color("#E6EDF3")).Background(lipgloss.Color("#0B1118")),
	NoBorder:      lipgloss.HiddenBorder(),
	Header:        lipgloss.NewStyle().Padding(0, 1),
	HeaderMeta:    lipgloss.NewStyle().Foreground(lipgloss.Color("#7D8590")),
	Title:         lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#F0F6FC")),
	Panel:         lipgloss.NewStyle().Padding(0, 1),
	PanelMuted:    lipgloss.NewStyle().Padding(0, 1).Foreground(lipgloss.Color("#7D8590")),
	Composer:      lipgloss.NewStyle().Padding(0, 1).BorderTop(true).BorderStyle(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("#253244")),
	Footer:        lipgloss.NewStyle().Padding(0, 1).Foreground(lipgloss.Color("#6E7681")),
	Overlay:       lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#2F81F7")).Background(lipgloss.Color("#111A23")).Padding(1, 2),
	OverlayBody:   lipgloss.NewStyle().Foreground(lipgloss.Color("#DCE6F2")),
	Text:          lipgloss.NewStyle().Foreground(lipgloss.Color("#DCE6F2")),
	TextMuted:     lipgloss.NewStyle().Foreground(lipgloss.Color("#7D8590")),
	User:          lipgloss.NewStyle().Foreground(lipgloss.Color("#7EE787")).Bold(true),
	Assistant:     lipgloss.NewStyle().Foreground(lipgloss.Color("#79C0FF")).Bold(true),
	Tool:          lipgloss.NewStyle().Foreground(lipgloss.Color("#F2CC60")).Bold(true),
	Note:          lipgloss.NewStyle().Foreground(lipgloss.Color("#A5A5A5")).Bold(true),
	Error:         lipgloss.NewStyle().Foreground(lipgloss.Color("#FF7B72")).Bold(true),
	Selected:      lipgloss.NewStyle().Foreground(lipgloss.Color("#0B1118")).Background(lipgloss.Color("#8B949E")).Bold(true),
	SelectedBody:  lipgloss.NewStyle().Foreground(lipgloss.Color("#F0F6FC")),
	BadgeMuted:    lipgloss.NewStyle().Foreground(lipgloss.Color("#7D8590")),
	StatusRunning: lipgloss.NewStyle().Foreground(lipgloss.Color("#79C0FF")),
	StatusDone:    lipgloss.NewStyle().Foreground(lipgloss.Color("#7EE787")),
	StatusFailed:  lipgloss.NewStyle().Foreground(lipgloss.Color("#FF7B72")),
}

var shellSpin = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func (m shellModel) renderHeader() string {
	st := m.state()
	state := "idle"
	if m.busy {
		state = shellSpin[m.spinner%len(shellSpin)] + " running"
	}
	title := shellTheme.Title.Render("Mink")
	line1 := lipgloss.JoinHorizontal(lipgloss.Left, title, shellTheme.HeaderMeta.Render("  "+state))
	line2 := shellTheme.HeaderMeta.Render(fmt.Sprintf("runtime %s   model %s   session %s   cwd %s", st.Runtime, st.Model, st.Session, st.Cwd))
	return shellTheme.Header.Width(m.width).Render(line1 + "\n" + line2)
}

func (m shellModel) renderTranscript() string {
	if m.viewport.Width <= 0 || m.viewport.Height <= 0 {
		return ""
	}
	title := shellTheme.BadgeMuted.Render("Transcript")
	body := m.viewport.View()
	if strings.TrimSpace(body) == "" {
		body = shellTheme.PanelMuted.Render("No messages yet.\n\nStart with a task, a question, or a local command like !help.")
	}
	box := title + "\n" + shellTheme.Panel.Width(m.width).Height(m.viewport.Height).Render(body)
	return shellTheme.Panel.Width(m.width).Height(m.viewport.Height + 1).Render(box)
}

func (m shellModel) renderComposer() string {
	focus := "transcript"
	if m.focus == focusComposer {
		focus = "composer"
	}
	title := shellTheme.BadgeMuted.Render("Composer") + shellTheme.HeaderMeta.Render("   focus "+focus)
	input := m.input.View()
	body := title + "\n" + input
	return shellTheme.Composer.Width(m.width).Render(body)
}

func (m shellModel) renderFooter() string {
	if m.overlay == overlayApproval {
		return shellTheme.Footer.Width(m.width).Render("Approve with y / a / n. Press Esc to deny.")
	}
	if m.overlay == overlayDetail {
		return shellTheme.Footer.Width(m.width).Render("Esc closes details.")
	}
	if m.overlay == overlayHelp {
		return shellTheme.Footer.Width(m.width).Render("Esc closes help.")
	}
	return shellTheme.Footer.Width(m.width).Render("Ctrl+S send   Tab switch focus   Ctrl+O details   Ctrl+G help   Ctrl+C quit")
}

func (m shellModel) renderItem(item *chatItem, idx int) []string {
	if item == nil {
		return nil
	}
	head := item.title()
	switch item.Kind {
	case itemUser:
		head = shellTheme.User.Render(head)
	case itemAssistant:
		head = shellTheme.Assistant.Render(head)
	case itemTool:
		head = shellTheme.Tool.Render(head)
	case itemNotice:
		head = shellTheme.Note.Render(head)
	case itemError:
		head = shellTheme.Error.Render(head)
	}
	if idx == m.selected {
		head = shellTheme.Selected.Render(" " + textutil.Preview(textutil.Valid(stripANSI(head)), max(8, m.viewport.Width-4)) + " ")
	}
	lines := []string{head}
	for _, line := range wrapDisplay(textutil.Valid(item.Content), max(12, m.viewport.Width-4)) {
		if idx == m.selected {
			lines = append(lines, shellTheme.SelectedBody.Render("  "+line))
			continue
		}
		lines = append(lines, shellTheme.Text.Render("  "+line))
	}
	return lines
}

func (m shellModel) detailBody() string {
	if m.selected < 0 || m.selected >= len(m.items) {
		return "No item selected."
	}
	return strings.Join(wrapDisplay(m.items[m.selected].detailText(), max(16, m.width-18)), "\n")
}

func (m shellModel) approvalBody() string {
	if len(m.approvals) == 0 {
		return "No pending approvals."
	}
	req := m.approvals[0].req
	body := fmt.Sprintf("Action\n%s\n\nPattern\n%s\n\nPress y to allow once, a to allow always, n to deny.", req.Action, req.Pattern)
	return strings.Join(wrapDisplay(body, max(16, m.width-18)), "\n")
}

func (m shellModel) helpBody() string {
	text := []string{
		"Composer",
		"Ctrl+S send the current input.",
		"Tab switches between composer and transcript.",
		"",
		"Transcript",
		"j / k or arrow keys move the selection.",
		"Enter or Ctrl+O opens details for the selected item.",
		"",
		"Panels",
		"Ctrl+G opens this help panel.",
		"Esc closes overlays or returns focus to the composer.",
	}
	return strings.Join(text, "\n")
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

func stripANSI(s string) string {
	var b strings.Builder
	skip := false
	for _, r := range s {
		switch {
		case r == '\x1b':
			skip = true
		case skip && r == 'm':
			skip = false
		case !skip:
			b.WriteRune(r)
		}
	}
	return b.String()
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
