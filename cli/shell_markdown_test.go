package cli

import (
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"
)

func TestRenderMarkdownRendersTablesLegibly(t *testing.T) {
	lines := renderMarkdown(`| 问题 | 位置 | 建议 |
|------|------|------|
| **28MB binary 在仓库根目录** | ./sumi | .gitignore 里加 /sumi |`, 80)
	out := strings.Join(lines, "\n")

	for _, want := range []string{"问题", "位置", "建议", "28MB binary 在仓库根目录", "./sumi"} {
		if !strings.Contains(out, want) {
			t.Fatalf("table rendering lost %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "|------|") || strings.Contains(out, "**28MB") {
		t.Fatalf("table rendering leaked markdown syntax:\n%s", out)
	}
}

func TestRenderMarkdownMixesTextAndTables(t *testing.T) {
	lines := renderMarkdown(`## 优化建议

先修最痛的。

| 包 | 风险 |
|----|------|
| config | 配置入口 |

后面再补测试。`, 72)
	out := strings.Join(lines, "\n")

	for _, want := range []string{"优化建议", "先修最痛的。", "包", "风险", "config", "后面再补测试。"} {
		if !strings.Contains(out, want) {
			t.Fatalf("mixed rendering lost %q:\n%s", want, out)
		}
	}
}

func TestRenderMarkdownTableFitsWidth(t *testing.T) {
	lines := renderMarkdown(`| 问题 | 位置 | 建议 |
|------|------|------|
| **28MB binary 在仓库根目录** | ./sumi | .gitignore 里加 /sumi，已有 ./bin/ 就够了 |`, 40)

	for _, line := range lines {
		if w := runewidth.StringWidth(line); w > 40 {
			t.Fatalf("line width = %d, want <= 40: %q\n%s", w, line, strings.Join(lines, "\n"))
		}
	}
}
