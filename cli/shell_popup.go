package cli

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

const popupBg = lipgloss.Color("235")
const popupSelectedBg = lipgloss.Color("236")

type popupItem struct {
	Title string
	Meta  string
	Desc  string
}

type popupList struct {
	Title    string
	Hint     string
	Query    string
	Empty    string
	Items    []popupItem
	Selected int
}

func (m shellModel) renderPopupList(base string, p popupList) string {
	w := min(max(56, m.width*2/3), max(20, m.width-4))
	h := min(max(12, m.height*2/3), max(8, m.height-4))
	inner := max(20, w-4)
	bodyH := max(1, h-4)

	head := popupLine(inner, lipgloss.Color("6"), popupBg).Render(alignFooter(p.Title, p.Hint, inner))
	body := strings.Join(popupListLines(p, inner, bodyH), "\n")
	panel := shellTheme.Overlay.Width(w).Height(h).Render(head + "\n\n" + body)
	x := max(0, (m.width-lipgloss.Width(panel))/2)
	y := max(0, (m.height-lipgloss.Height(panel))/2)
	return placeOverlay(base, panel, x, y)
}

func popupListLines(p popupList, width, height int) []string {
	var lines []string
	if q := strings.TrimSpace(p.Query); q != "" {
		lines = append(lines, popupLine(width, lipgloss.Color("8"), popupBg).Render("filter "+q), popupLine(width, lipgloss.Color("8"), popupBg).Render(""))
	}
	room := max(1, height-len(lines))
	if len(p.Items) == 0 {
		empty := strings.TrimSpace(p.Empty)
		if empty == "" {
			empty = "No items."
		}
		lines = append(lines, popupLine(width, lipgloss.Color("8"), popupBg).Render(empty))
		return fitPopupLines(lines, height)
	}

	rows := 2
	visible := max(1, room/rows)
	if visible > len(p.Items) {
		visible = len(p.Items)
	}
	selected := clamp(p.Selected, 0, len(p.Items)-1)
	start := selected - visible + 1
	if start < 0 {
		start = 0
	}
	if start+visible > len(p.Items) {
		start = len(p.Items) - visible
	}
	if start > 0 {
		lines = append(lines, popupLine(width, lipgloss.Color("8"), popupBg).Render("..."))
	}
	for i := start; i < len(p.Items) && i < start+visible; i++ {
		item := p.Items[i]
		prefix := "  "
		fg := lipgloss.Color("8")
		bg := popupBg
		if i == selected {
			prefix = "› "
			fg = lipgloss.Color("6")
			bg = popupSelectedBg
		}
		title := prefix + runewidth.Truncate(item.Title, max(8, width-18), "…")
		lines = append(lines, popupLine(width, fg, bg).Render(alignFooter(title, item.Meta, width)))
		if desc := strings.TrimSpace(item.Desc); desc != "" {
			lines = append(lines, popupLine(width, lipgloss.Color("8"), bg).Render("    "+runewidth.Truncate(desc, max(8, width-4), "…")))
		}
	}
	if start+visible < len(p.Items) {
		lines = append(lines, popupLine(width, lipgloss.Color("8"), popupBg).Render("..."))
	}
	return fitPopupLines(lines, height)
}

func fitPopupLines(lines []string, height int) []string {
	if height <= 0 {
		return nil
	}
	if len(lines) > height {
		return lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, popupLine(1, lipgloss.Color("8"), popupBg).Render(""))
	}
	return lines
}

func popupLine(width int, fg, bg lipgloss.Color) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(fg).Background(bg).Width(max(1, width))
}
