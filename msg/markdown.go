package msg

import (
	"regexp"
	"strings"
)

var inlineHeadingRE = regexp.MustCompile(`^(.*?[^#\s])(\s*)(#{1,6}[ \t].*)$`)

func NormalizeMarkdown(src string) string {
	out := splitInlineHeadings(src)
	out = repairTables(out)
	out = dedupeAdjacentHeadingSections(out)
	return out
}

func splitInlineHeadings(src string) string {
	lines := strings.Split(src, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		m := inlineHeadingRE.FindStringSubmatch(line)
		if m == nil {
			out = append(out, line)
			continue
		}
		prefix := strings.TrimRight(m[1], " \t")
		heading := m[3]
		if prefix == "" {
			out = append(out, line)
			continue
		}
		out = append(out, prefix, "", heading)
	}
	return strings.Join(out, "\n")
}

func repairTables(src string) string {
	lines := strings.Split(src, "\n")
	for i := 0; i+1 < len(lines); i++ {
		header := lines[i]
		sep := lines[i+1]
		if !isTableRow(header) || !isSeparatorRow(sep) {
			continue
		}
		headerCols := countTableCols(header)
		sepCols := countTableCols(sep)
		if headerCols > sepCols {
			lines[i+1] = padSeparator(sep, headerCols)
		}
	}
	return strings.Join(lines, "\n")
}

func dedupeAdjacentHeadingSections(src string) string {
	lines := strings.Split(src, "\n")
	if len(lines) < 4 {
		return src
	}
	for first := 0; first < len(lines); first++ {
		if !isHeadingLine(lines[first]) {
			continue
		}
		for second := first + 1; second < len(lines); second++ {
			if strings.TrimSpace(lines[second]) != strings.TrimSpace(lines[first]) {
				continue
			}
			a := strings.TrimSpace(strings.Join(lines[first:second], "\n"))
			b := strings.TrimSpace(strings.Join(lines[second:], "\n"))
			if len(a) < 200 || a != b {
				continue
			}
			return strings.Join(lines[:second], "\n")
		}
	}
	return src
}

func isHeadingLine(line string) bool {
	t := strings.TrimSpace(line)
	if !strings.HasPrefix(t, "#") {
		return false
	}
	n := 0
	for n < len(t) && t[n] == '#' {
		n++
	}
	return n >= 1 && n <= 6 && n < len(t) && (t[n] == ' ' || t[n] == '\t')
}

func isTableRow(line string) bool {
	t := strings.TrimSpace(line)
	return strings.HasPrefix(t, "|") && strings.HasSuffix(t, "|") && len(t) > 1
}

func isSeparatorRow(line string) bool {
	t := strings.TrimSpace(line)
	if !isTableRow(t) {
		return false
	}
	cells := strings.Split(strings.Trim(t, "|"), "|")
	for _, c := range cells {
		c = strings.TrimSpace(c)
		if c == "" {
			return false
		}
		for i, r := range c {
			if r == '-' {
				continue
			}
			if r == ':' && (i == 0 || i == len(c)-1) {
				continue
			}
			return false
		}
	}
	return true
}

func countTableCols(line string) int {
	t := strings.Trim(strings.TrimSpace(line), "|")
	if t == "" {
		return 0
	}
	return len(strings.Split(t, "|"))
}

func padSeparator(sep string, target int) string {
	leading := sep[:len(sep)-len(strings.TrimLeft(sep, " \t"))]
	t := strings.Trim(strings.TrimSpace(sep), "|")
	cells := strings.Split(t, "|")
	for len(cells) < target {
		cells = append(cells, "---")
	}
	return leading + "|" + strings.Join(cells, "|") + "|"
}
