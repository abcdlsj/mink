package telegram

import (
	"strings"
	"testing"
)

func TestRenderTelegramHTMLHeading(t *testing.T) {
	got := renderTelegramHTML("## 标题\n\nhello **sumi**")
	want := "<b>标题</b>\n\nhello <b>sumi</b>"
	if got != want {
		t.Fatalf("markdown = %q", got)
	}
}

func TestRenderTelegramHTMLEscapesText(t *testing.T) {
	got := renderTelegramHTML("a < b & `x < y`")
	want := "a &lt; b &amp; <code>x &lt; y</code>"
	if got != want {
		t.Fatalf("markdown = %q", got)
	}
}

func TestRenderTelegramHTMLTable(t *testing.T) {
	got := renderTelegramHTML(`| 问题 | 位置 |
| --- | --- |
| heading 没展示 | tg |`)
	if !strings.HasPrefix(got, "<pre>") || !strings.HasSuffix(got, "</pre>") {
		t.Fatalf("table not rendered as pre: %q", got)
	}
	for _, s := range []string{"问题", "位置", "heading 没展示", "----"} {
		if !strings.Contains(got, s) {
			t.Fatalf("missing %q in %q", s, got)
		}
	}
}

func TestRenderTelegramHTMLLink(t *testing.T) {
	got := renderTelegramHTML("[sumi](https://example.com?a=1&b=2)")
	want := `<a href="https://example.com?a=1&amp;b=2">sumi</a>`
	if got != want {
		t.Fatalf("markdown = %q", got)
	}
}
