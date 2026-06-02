package space

import (
	"reflect"
	"testing"
)

func testResolver() PersonaResolver {
	return ResolverFromPersonas([]PersonaInfo{
		{ID: "coder", Display: "Coder"},
		{ID: "reviewer", Display: "Reviewer"},
		{ID: "tshoot", Display: "Tshoot"},
	})
}

func TestParseMentions(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"english bare", "@coder look", []string{"coder"}},
		{"display lowercase ok", "@Coder look", []string{"coder"}},
		{"display mixedcase ok", "@CODER look", []string{"coder"}},
		{"comma-prefixed", "ok, @reviewer take a pass", []string{"reviewer"}},

		{"cn space-prefixed", "请 @coder 看下这段", []string{"coder"}},
		{"cn paren-prefixed", "（@coder）看下", []string{"coder"}},
		{"cn comma-prefixed", "好，@reviewer 继续", []string{"reviewer"}},
		{"cn period-prefixed", "完事。@coder 接力", []string{"coder"}},

		{"multiple distinct", "@coder and @reviewer please", []string{"coder", "reviewer"}},
		{"dedup repeats", "@coder @coder @coder", []string{"coder"}},
		{"multiple keeps order", "@reviewer first then @coder", []string{"reviewer", "coder"}},

		{"unknown drop", "@nobody hi", nil},
		{"mixed known unknown", "@nobody and @coder", []string{"coder"}},

		{"email-like glued", "ping me at andy@coder.dev", nil},
		{"glued letter prefix", "abc@coder", nil},
		{"start of line", "@coder hello", []string{"coder"}},
		{"newline-prefixed", "line1\n@coder line2", []string{"coder"}},
		{"tab-prefixed", "go\t@coder", []string{"coder"}},

		{"trailing comma", "@coder, please", []string{"coder"}},
		{"trailing chinese punct", "@coder。请看", []string{"coder"}},
		{"trailing slash", "@coder/今天", []string{"coder"}},

		{"inline code escape", "use `@coder` syntax", nil},
		{"inline code mixed", "use `@coder` and ping @reviewer", []string{"reviewer"}},
		{"fenced code block", "```\n@coder do it\n```", nil},
		{"fenced then live", "```\n@coder\n```\nactually @reviewer please", []string{"reviewer"}},

		{"empty", "", nil},
		{"@-only", "@", nil},
		{"@ space", "@ coder", nil},
		{"@@", "@@coder", nil},
	}
	resolver := testResolver()
	for _, c := range cases {
		got := ParseMentions(c.in, resolver, 4)
		if !sliceEq(got, c.want) {
			t.Errorf("%s: ParseMentions(%q) = %v, want %v", c.name, c.in, got, c.want)
		}
	}
}

func TestParseMentionsRespectsMaxCap(t *testing.T) {
	in := "@coder and @reviewer and @tshoot please"
	got := ParseMentions(in, testResolver(), 2)
	if !reflect.DeepEqual(got, []string{"coder", "reviewer"}) {
		t.Errorf("max=2 should clip, got %v", got)
	}
}

func TestParseMentionsZeroMaxIsUnbounded(t *testing.T) {
	in := "@coder @reviewer @tshoot"
	got := ParseMentions(in, testResolver(), 0)
	if !reflect.DeepEqual(got, []string{"coder", "reviewer", "tshoot"}) {
		t.Errorf("max=0 should accept all, got %v", got)
	}
}

func TestParseMentionsIgnoresNilResolver(t *testing.T) {
	got := ParseMentions("@coder", nil, 4)
	if got != nil {
		t.Errorf("nil resolver should yield nil, got %v", got)
	}
}

func TestResolverFromPersonas(t *testing.T) {
	r := testResolver()
	if id, ok := r("coder"); !ok || id != "coder" {
		t.Errorf("id match failed: %q / %v", id, ok)
	}
	if id, ok := r("Coder"); !ok || id != "coder" {
		t.Errorf("display lowercase failed: %q / %v", id, ok)
	}
	if id, ok := r("CODER"); !ok || id != "coder" {
		t.Errorf("display upper failed: %q / %v", id, ok)
	}
	if _, ok := r("nope"); ok {
		t.Error("unknown should not resolve")
	}
	if _, ok := r(""); ok {
		t.Error("empty should not resolve")
	}
}

func sliceEq(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}
