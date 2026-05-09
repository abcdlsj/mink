package textutil

import "testing"

func TestValidReplacesInvalidUTF8(t *testing.T) {
	in := "ab\xffcd"
	got := Valid(in)
	if got != "ab\uFFFDcd" {
		t.Fatalf("Valid() = %q", got)
	}
}

func TestEllipsisUsesDisplayWidth(t *testing.T) {
	if got := Ellipsis("你好吗世界", 4); got != "你…" {
		t.Fatalf("Ellipsis(cjk, 4) = %q, want \"你…\"", got)
	}
	if got := Ellipsis("hello world", 8); got != "hello w…" {
		t.Fatalf("Ellipsis(ascii, 8) = %q, want \"hello w…\"", got)
	}
	if got := Ellipsis("你好", 4); got != "你好" {
		t.Fatalf("Ellipsis(fits, 4) = %q, want \"你好\"", got)
	}
}
