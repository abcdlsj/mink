package platform

import (
	"html"
	"regexp"
	"strconv"
	"strings"
)

var (
	tgCodeSpanRe         = regexp.MustCompile("`([^`\\n]+)`")
	tgLinkRe             = regexp.MustCompile(`\[([^\]\n]+)\]\((https?://[^)\s]+)\)`)
	tgBoldAsteriskRe     = regexp.MustCompile(`\*\*([^*\n][^*\n]*?)\*\*`)
	tgBoldUnderscoreRe   = regexp.MustCompile(`__([^_\n][^_\n]*?)__`)
	tgStrikethroughRe    = regexp.MustCompile(`~~([^~\n][^~\n]*?)~~`)
	tgFenceMarkerPrefix  = "```"
	tgCodePlaceholderTag = "{{TGCODE"
)

func tgRenderText(raw string) string {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\r", "\n")

	lines := strings.Split(raw, "\n")
	if len(lines) == 0 {
		return ""
	}

	var b strings.Builder
	inFence := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, tgFenceMarkerPrefix) {
			if inFence {
				b.WriteString("</pre>")
			} else {
				b.WriteString("<pre>")
			}
			inFence = !inFence
		} else if inFence {
			b.WriteString(html.EscapeString(line))
		} else {
			b.WriteString(tgRenderInline(line))
		}
		if i < len(lines)-1 {
			b.WriteByte('\n')
		}
	}

	if inFence {
		b.WriteString("</pre>")
	}

	return b.String()
}

func tgRenderInline(line string) string {
	if line == "" {
		return ""
	}

	escaped := html.EscapeString(line)
	placeholders := make([]string, 0)
	escaped = tgCodeSpanRe.ReplaceAllStringFunc(escaped, func(m string) string {
		code := m[1 : len(m)-1]
		token := tgCodePlaceholderTag + strconv.Itoa(len(placeholders)) + "}}"
		placeholders = append(placeholders, "<code>"+code+"</code>")
		return token
	})

	escaped = tgLinkRe.ReplaceAllString(escaped, `<a href="$2">$1</a>`)
	escaped = tgBoldAsteriskRe.ReplaceAllString(escaped, `<b>$1</b>`)
	escaped = tgBoldUnderscoreRe.ReplaceAllString(escaped, `<b>$1</b>`)
	escaped = tgStrikethroughRe.ReplaceAllString(escaped, `<s>$1</s>`)

	for i, code := range placeholders {
		token := tgCodePlaceholderTag + strconv.Itoa(i) + "}}"
		escaped = strings.ReplaceAll(escaped, token, code)
	}

	return escaped
}
