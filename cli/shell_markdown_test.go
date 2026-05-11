package cli

import (
	"strings"
	"testing"
)

func TestRenderMarkdownKeepsTablesPlain(t *testing.T) {
	lines := renderMarkdown(`| 问题 | 位置 | 建议 |
|------|------|------|
| **28MB binary 在仓库根目录** | ./sumi | .gitignore 里加 /sumi |`, 80)
	out := strings.Join(lines, "\n")

	for _, want := range []string{"| 问题 | 位置 | 建议 |", "|------|------|------|", "| **28MB binary 在仓库根目录** |"} {
		if !strings.Contains(out, want) {
			t.Fatalf("table rendering lost %q:\n%s", want, out)
		}
	}
}
