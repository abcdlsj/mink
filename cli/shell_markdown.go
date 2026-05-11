package cli

import (
	"strings"
	"sync"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
	mdstyles "github.com/charmbracelet/glamour/styles"

	"github.com/abcdlsj/sumi/textutil"
)

var markdownCache sync.Map

func renderMarkdown(text string, width int) []string {
	text = strings.TrimSpace(textutil.Valid(text))
	if text == "" {
		return nil
	}
	if hasMarkdownTable(text) {
		return wrapDisplay(text, width)
	}
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

func ptr(s string) *string {
	return &s
}

func strPtr(s string) *string {
	return &s
}
