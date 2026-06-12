package msg

import (
	"strings"
	"testing"
)

func TestNormalizeMarkdownRepairsInlineHeadingAndTable(t *testing.T) {
	in := "找到了。## 日志查询结果\n\n| 项目 | 配置 |\n|---|\n| OS | Ubuntu |"
	got := NormalizeMarkdown(in)
	if !strings.Contains(got, "找到了。\n\n## 日志查询结果") {
		t.Fatalf("inline heading was not split:\n%s", got)
	}
	if !strings.Contains(got, "|---|---|") {
		t.Fatalf("table separator was not padded:\n%s", got)
	}
}

func TestNormalizeMarkdownDedupesRepeatedHeadingTail(t *testing.T) {
	section := "## 日志查询结果\n\n### 查询条件\n- 服务：`main.app-svr.app-opus`\n\n### 错误分组\n\n| 类型 | 数量 | 关键信息 |\n|---|---|---|\n| `dynBrief not found` | 5 | 同一 traceId |\n\n### 结论\n\n10 分钟窗口看下来没有异常。"
	in := "前面解释。\n\n" + section + "\n\n" + section
	got := NormalizeMarkdown(in)
	if strings.Count(got, "## 日志查询结果") != 1 {
		t.Fatalf("heading section count = %d, want 1:\n%s", strings.Count(got, "## 日志查询结果"), got)
	}
}
