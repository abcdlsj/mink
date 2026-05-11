package cli

import (
	"strings"
	"sync"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
	mdstyles "github.com/charmbracelet/glamour/styles"
	"github.com/mattn/go-runewidth"

	"github.com/abcdlsj/sumi/textutil"
)

var markdownCache sync.Map

func renderMarkdown(text string, width int) []string {
	text = strings.TrimSpace(textutil.Valid(text))
	if text == "" {
		return nil
	}
	if hasMarkdownTable(text) {
		return renderMarkdownBlocks(text, width)
	}
	return renderMarkdownText(text, width)
}

func renderMarkdownText(text string, width int) []string {
	if width < 12 {
		return wrapDisplay(text, width)
	}
	r, err := markdownRenderer(width)
	if err != nil {
		return wrapDisplay(text, width)
	}
	out, err := r.Render(text)
	if err != nil {
		return wrapDisplay(text, width)
	}
	return trimBlankLines(strings.Split(strings.TrimRight(out, "\n"), "\n"))
}

func renderMarkdownBlocks(text string, width int) []string {
	lines := strings.Split(textutil.Valid(text), "\n")
	var out []string
	var buf []string
	add := func(lines []string) {
		lines = trimBlankLines(lines)
		if len(lines) == 0 {
			return
		}
		if len(out) > 0 {
			out = append(out, "")
		}
		out = append(out, lines...)
	}
	flush := func() {
		if len(buf) == 0 {
			return
		}
		add(renderMarkdownText(strings.Join(buf, "\n"), width))
		buf = nil
	}
	for i := 0; i < len(lines); {
		if i+1 < len(lines) && strings.Contains(lines[i], "|") && markdownTableSep(lines[i+1]) {
			flush()
			start := i
			i += 2
			for i < len(lines) && strings.Contains(lines[i], "|") && strings.TrimSpace(lines[i]) != "" {
				i++
			}
			add(renderMarkdownTable(lines[start:i], width))
			continue
		}
		buf = append(buf, lines[i])
		i++
	}
	flush()
	return out
}

func markdownRenderer(width int) (*glamour.TermRenderer, error) {
	if v, ok := markdownCache.Load(width); ok {
		return v.(*glamour.TermRenderer), nil
	}
	style := markdownStyle()
	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(style),
		glamour.WithWordWrap(width),
		glamour.WithPreservedNewLines(),
	)
	if err != nil {
		return nil, err
	}
	markdownCache.Store(width, r)
	return r, nil
}

func markdownStyle() ansi.StyleConfig {
	s := mdstyles.TokyoNightStyleConfig
	zero := uint(0)
	one := uint(1)
	s.Document.Margin = &zero
	s.Paragraph.Margin = &zero
	s.BlockQuote.Margin = &zero
	s.CodeBlock.Margin = &zero
	s.List.Margin = &zero
	s.Heading.Margin = &zero
	s.Item.BlockPrefix = "• "
	s.Code.BackgroundColor = ptr("236")
	s.Code.Color = ptr("252")
	s.H1.BackgroundColor = ptr("31")
	s.H1.Color = ptr("231")
	s.H2.Color = ptr("117")
	s.Link.Color = ptr("117")
	s.LinkText.Color = ptr("159")
	s.BlockQuote.Indent = &one
	s.BlockQuote.IndentToken = strPtr("▎ ")
	return s
}

func trimBlankLines(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func indentLines(lines []string, prefix string) []string {
	if len(lines) == 0 {
		return nil
	}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			out = append(out, prefix)
			continue
		}
		out = append(out, prefix+line)
	}
	return out
}

func indentBlock(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}

func hasMarkdownTable(s string) bool {
	lines := strings.Split(textutil.Valid(s), "\n")
	for i := 0; i+1 < len(lines); i++ {
		if strings.Contains(lines[i], "|") && markdownTableSep(lines[i+1]) {
			return true
		}
	}
	return false
}

func markdownTableSep(s string) bool {
	s = strings.TrimSpace(s)
	if !strings.Contains(s, "|") || !strings.Contains(s, "-") {
		return false
	}
	for _, r := range s {
		switch r {
		case '|', ':', '-', ' ':
		default:
			return false
		}
	}
	return true
}

func renderMarkdownTable(lines []string, width int) []string {
	if len(lines) < 2 {
		return wrapDisplay(strings.Join(lines, "\n"), width)
	}
	rows := [][]string{tableCells(lines[0])}
	for _, line := range lines[2:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		rows = append(rows, tableCells(line))
	}
	cols := 0
	for _, row := range rows {
		cols = max(cols, len(row))
	}
	if cols == 0 {
		return nil
	}
	widths := tableWidths(rows, cols, width)
	var out []string
	out = append(out, renderTableRow(rows[0], widths)...)
	out = append(out, tableRule(widths))
	for _, row := range rows[1:] {
		out = append(out, renderTableRow(row, widths)...)
	}
	return out
}

func tableCells(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")
	parts := strings.Split(line, "|")
	out := make([]string, len(parts))
	for i, part := range parts {
		out[i] = cleanTableCell(part)
	}
	return out
}

func cleanTableCell(s string) string {
	s = strings.TrimSpace(textutil.CollapseSpace(s))
	for _, mark := range []string{"**", "__", "`"} {
		s = strings.ReplaceAll(s, mark, "")
	}
	return s
}

func tableWidths(rows [][]string, cols, width int) []int {
	widths := make([]int, cols)
	for _, row := range rows {
		for i := 0; i < cols; i++ {
			cell := ""
			if i < len(row) {
				cell = row[i]
			}
			widths[i] = max(widths[i], runewidth.StringWidth(cell))
		}
	}
	for i := range widths {
		widths[i] = clamp(widths[i], 4, 36)
	}
	available := max(cols*4, width-(cols-1)*2)
	for sumInts(widths) > available {
		i := widest(widths)
		if widths[i] <= 4 {
			break
		}
		widths[i]--
	}
	return widths
}

func renderTableRow(row []string, widths []int) []string {
	wrapped := make([][]string, len(widths))
	height := 1
	for i, width := range widths {
		cell := ""
		if i < len(row) {
			cell = row[i]
		}
		wrapped[i] = wrapDisplay(cell, width)
		height = max(height, len(wrapped[i]))
	}
	out := make([]string, 0, height)
	for line := 0; line < height; line++ {
		var b strings.Builder
		for col, width := range widths {
			if col > 0 {
				b.WriteString("  ")
			}
			cell := ""
			if line < len(wrapped[col]) {
				cell = wrapped[col][line]
			}
			b.WriteString(padRight(cell, width))
		}
		out = append(out, strings.TrimRight(b.String(), " "))
	}
	return out
}

func tableRule(widths []int) string {
	var b strings.Builder
	for i, width := range widths {
		if i > 0 {
			b.WriteString("  ")
		}
		b.WriteString(strings.Repeat("─", width))
	}
	return b.String()
}

func padRight(s string, width int) string {
	w := runewidth.StringWidth(s)
	if w >= width {
		return s
	}
	return s + strings.Repeat(" ", width-w)
}

func sumInts(xs []int) int {
	var n int
	for _, x := range xs {
		n += x
	}
	return n
}

func widest(xs []int) int {
	var idx int
	for i, x := range xs {
		if x > xs[idx] {
			idx = i
		}
	}
	return idx
}

func ptr(s string) *string {
	return &s
}

func strPtr(s string) *string {
	return &s
}
