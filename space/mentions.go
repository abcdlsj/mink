package space

import (
	"strings"
	"unicode"
)

// PersonaResolver looks up a persona by id or display name. Returns
// the canonical id when found, or "" if no match. Display matches
// are case-insensitive; id matches must be exact.
type PersonaResolver func(token string) (id string, ok bool)

// ParseMentions extracts @-mentions from a message body and resolves
// each one against the supplied resolver. The result is a stable,
// de-duplicated, capped list of canonical persona ids in order of
// first appearance.
//
// Per Iris's amendments:
//   - Code blocks (```...```) and inline code (`...`) are skipped so
//     that @-symbols inside code are never routed.
//   - Both English and Chinese contexts are handled: a mention is
//     only recognized when the @ is at the start of input or
//     preceded by whitespace / common punctuation in either script.
//   - Tokens that do not resolve are silently dropped (treated as
//     plain text), matching the proposal's "unknown @ is not a
//     wake-up" rule.
//
// max bounds the number of mentions per message; pass 0 for "no
// cap" but the routing layer should always pass a finite number.
func ParseMentions(text string, resolver PersonaResolver, max int) []string {
	if text == "" || resolver == nil {
		return nil
	}
	stripped := stripCode(text)
	out := make([]string, 0, 4)
	seen := map[string]bool{}
	runes := []rune(stripped)
	for i := 0; i < len(runes); i++ {
		if runes[i] != '@' {
			continue
		}
		if !isMentionStart(runes, i) {
			continue
		}
		token, next := readMentionToken(runes, i+1)
		if token == "" {
			i = next - 1
			continue
		}
		i = next - 1
		id, ok := resolver(token)
		if !ok {
			continue
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
		if max > 0 && len(out) >= max {
			break
		}
	}
	return out
}

// stripCode returns text with fenced blocks and inline backticks
// replaced by spaces of equal length so that index positions stay
// aligned with the original input. The mention parser only needs
// the @-tokens, so spaces are good enough.
func stripCode(text string) string {
	out := []rune(text)
	// Fenced code blocks ```...```
	for {
		start := indexRune(out, "```")
		if start < 0 {
			break
		}
		end := indexRuneFrom(out, "```", start+3)
		if end < 0 {
			end = len(out)
		} else {
			end += 3
		}
		blank(out, start, end)
		if end >= len(out) {
			break
		}
	}
	// Inline `code`
	for i := 0; i < len(out); i++ {
		if out[i] != '`' {
			continue
		}
		j := i + 1
		for j < len(out) && out[j] != '`' && out[j] != '\n' {
			j++
		}
		if j >= len(out) || out[j] != '`' {
			continue
		}
		blank(out, i, j+1)
		i = j
	}
	return string(out)
}

func indexRune(rs []rune, sub string) int     { return runesIndex(rs, []rune(sub), 0) }
func indexRuneFrom(rs []rune, sub string, from int) int {
	return runesIndex(rs, []rune(sub), from)
}

func runesIndex(rs []rune, sub []rune, from int) int {
	if len(sub) == 0 || from >= len(rs) {
		return -1
	}
	for i := from; i+len(sub) <= len(rs); i++ {
		match := true
		for k := 0; k < len(sub); k++ {
			if rs[i+k] != sub[k] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

func blank(rs []rune, lo, hi int) {
	for i := lo; i < hi && i < len(rs); i++ {
		if rs[i] == '\n' {
			continue
		}
		rs[i] = ' '
	}
}

// isMentionStart reports whether the @ at runes[i] can begin a
// mention (i.e. not glued to an identifier on its left).
func isMentionStart(runes []rune, i int) bool {
	if i == 0 {
		return true
	}
	prev := runes[i-1]
	if unicode.IsSpace(prev) {
		return true
	}
	switch prev {
	case '(', '（', '[', '【', '{', '「', '『',
		',', '，', '.', '。', ';', '；', ':', '：',
		'!', '！', '?', '？', '/', '\\', '\n', '\r', '\t':
		return true
	}
	// Letters / digits glued before @ → not a mention (e.g. email)
	return false
}

// readMentionToken returns the token following @ and the index just
// after it. Tokens contain ASCII letters, digits, '_' and '-'.
// Display-name matching with embedded spaces is intentionally not
// supported in v1; the resolver may map ids only.
func readMentionToken(runes []rune, start int) (string, int) {
	end := start
	for end < len(runes) {
		r := runes[end]
		if isTokenRune(r) {
			end++
			continue
		}
		break
	}
	if end == start {
		return "", end
	}
	return string(runes[start:end]), end
}

func isTokenRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z':
		return true
	case r >= 'A' && r <= 'Z':
		return true
	case r >= '0' && r <= '9':
		return true
	case r == '_' || r == '-':
		return true
	}
	return false
}

// ResolverFromPersonas builds a PersonaResolver that matches an id
// exactly or a display name case-insensitively against a slice of
// known personas.
func ResolverFromPersonas(personas []PersonaInfo) PersonaResolver {
	byID := make(map[string]string, len(personas))
	byDisplay := make(map[string]string, len(personas))
	for _, p := range personas {
		id := strings.TrimSpace(p.ID)
		if id == "" {
			continue
		}
		byID[id] = id
		if d := strings.TrimSpace(p.Display); d != "" {
			byDisplay[strings.ToLower(d)] = id
		}
	}
	return func(token string) (string, bool) {
		token = strings.TrimSpace(token)
		if token == "" {
			return "", false
		}
		if id, ok := byID[token]; ok {
			return id, true
		}
		if id, ok := byDisplay[strings.ToLower(token)]; ok {
			return id, true
		}
		return "", false
	}
}
