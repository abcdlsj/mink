package textutil

import "testing"

func TestValidReplacesInvalidUTF8(t *testing.T) {
	in := "ab\xffcd"
	got := Valid(in)
	if got != "ab\uFFFDcd" {
		t.Fatalf("Valid() = %q", got)
	}
}

func TestEllipsisUsesRunes(t *testing.T) {
	got := Ellipsis("你好吗世界", 4)
	if got != "你好吗…" {
		t.Fatalf("Ellipsis() = %q", got)
	}
}
