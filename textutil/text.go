package textutil

import (
	"strings"
	"unicode/utf8"
)

func Valid(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for len(s) > 0 {
		r, size := utf8.DecodeRuneInString(s)
		if r == utf8.RuneError && size == 1 {
			b.WriteRune('\uFFFD')
			s = s[1:]
			continue
		}
		b.WriteRune(r)
		s = s[size:]
	}
	return b.String()
}

func CollapseSpace(s string) string {
	return strings.Join(strings.Fields(Valid(strings.TrimSpace(s))), " ")
}

func ClipRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	rs := []rune(Valid(s))
	if len(rs) <= n {
		return string(rs)
	}
	return string(rs[:n])
}

func Ellipsis(s string, n int) string {
	if n <= 0 {
		return ""
	}
	rs := []rune(Valid(s))
	if len(rs) <= n {
		return string(rs)
	}
	if n == 1 {
		return "…"
	}
	return string(rs[:n-1]) + "…"
}

func Preview(s string, n int) string {
	return Ellipsis(CollapseSpace(s), n)
}
