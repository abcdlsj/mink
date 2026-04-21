package memory

import (
	"fmt"
	"strings"

	"github.com/abcdlsj/mink/textutil"
)

func render(mode string, scopes []scope, docs []doc) string {
	var sb strings.Builder
	sb.WriteString("Memory " + mode)
	if len(scopes) > 0 {
		var parts []string
		for _, sc := range scopes {
			parts = append(parts, scopeText(sc))
		}
		sb.WriteString(" [")
		sb.WriteString(strings.Join(parts, ", "))
		sb.WriteString("]")
	}
	sb.WriteByte('\n')
	if len(docs) == 0 {
		sb.WriteString("no memory docs")
		return sb.String()
	}
	for _, d := range docs {
		line := blank(d.Summary, summarize(d.Body, 160))
		fmt.Fprintf(&sb, "- %s (%s): %s\n", d.Title, scopeText(scope{Kind: d.ScopeKind, Key: d.ScopeKey}), line)
	}
	return strings.TrimSpace(sb.String())
}

func summarize(s string, n int) string {
	return textutil.Ellipsis(strings.Join(strings.Fields(strings.TrimSpace(s)), " "), n)
}

func blank(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return strings.TrimSpace(s)
}

func defaultLimit(n, fallback int) int {
	if n > 0 {
		return n
	}
	return fallback
}

func parseError(name string, err error) error {
	return fmt.Errorf("%s: parse error: %w", name, err)
}
