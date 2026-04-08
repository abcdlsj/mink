package platform

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	styleDiffAdd    = lipgloss.NewStyle().Foreground(lipgloss.Color("#A6E3A1")).Bold(true)
	styleDiffDel    = lipgloss.NewStyle().Foreground(lipgloss.Color("#F38BA8")).Bold(true)
	styleDiffHeader = lipgloss.NewStyle().Foreground(lipgloss.Color("#89B4FA")).Bold(true)
	styleDiffHunk   = lipgloss.NewStyle().Foreground(lipgloss.Color("#94E2D5")).Faint(true)

	ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)
)

func renderMarkdown(s string) string {
	if s == "" {
		return ""
	}

	lines := strings.Split(s, "\n")
	inCode := false
	codeLang := ""
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			if inCode {
				inCode = false
				codeLang = ""
				lines[i] = styleDim.Render("└─")
			} else {
				inCode = true
				codeLang = strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
				if codeLang != "" {
					lines[i] = styleDim.Render("┌─ " + codeLang)
				} else {
					lines[i] = styleDim.Render("┌─ code")
				}
			}
			continue
		}

		if inCode {
			lines[i] = renderCodeLine(line, codeLang)
			continue
		}

		if after, ok := strings.CutPrefix(trimmed, "# "); ok {
			lines[i] = styleBold.Render(after)
			continue
		}
		if after, ok := strings.CutPrefix(trimmed, "## "); ok {
			lines[i] = styleBold.Render(after)
			continue
		}
		if after, ok := strings.CutPrefix(trimmed, "### "); ok {
			lines[i] = styleBold.Render(after)
			continue
		}

		lines[i] = renderInlineMarkdown(line)
	}

	return strings.Join(lines, "\n")
}

func renderCodeLine(line, lang string) string {
	if lang != "diff" && lang != "patch" {
		return styleCode.Render(line)
	}
	trimmed := strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(trimmed, "diff "),
		strings.HasPrefix(trimmed, "+++"),
		strings.HasPrefix(trimmed, "---"):
		return styleDiffHeader.Render(line)
	case strings.HasPrefix(trimmed, "@@"):
		return styleDiffHunk.Render(line)
	case strings.HasPrefix(trimmed, "+"):
		return styleDiffAdd.Render(line)
	case strings.HasPrefix(trimmed, "-"):
		return styleDiffDel.Render(line)
	default:
		return styleCode.Render(line)
	}
}

func renderInlineMarkdown(line string) string {
	line = stripANSI(line)

	var b strings.Builder
	r := []rune(line)

	for i := 0; i < len(r); {
		if r[i] == '`' {
			j := i + 1
			for j < len(r) && r[j] != '`' {
				j++
			}
			if j < len(r) {
				b.WriteString(styleCode.Render(string(r[i+1 : j])))
				i = j + 1
				continue
			}
		}

		if i+1 < len(r) && r[i] == '*' && r[i+1] == '*' {
			j := i + 2
			for j+1 < len(r) {
				if r[j] == '*' && r[j+1] == '*' {
					break
				}
				j++
			}
			if j+1 < len(r) {
				b.WriteString(styleBold.Render(string(r[i+2 : j])))
				i = j + 2
				continue
			}
		}

		b.WriteRune(r[i])
		i++
	}

	return b.String()
}

func fmtTokens(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return "…"
	}
	return string(r[:n-1]) + "…"
}

func stripANSI(s string) string {
	return ansiRE.ReplaceAllString(s, "")
}
