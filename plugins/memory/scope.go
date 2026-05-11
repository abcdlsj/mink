package memory

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/abcdlsj/sumi/command"
)

func (s *store) resolveReadScope(ctx context.Context, src, kind, key string) scope {
	if strings.TrimSpace(kind) != "" {
		return s.scope(ctx, src, kind, key)
	}
	if p := command.PersonaFrom(ctx); p != "" {
		return scope{Kind: "persona", Key: p}
	}
	if strings.TrimSpace(src) != "" {
		return scope{Kind: "channel", Key: strings.TrimSpace(src)}
	}
	if strings.TrimSpace(s.workspace) != "" {
		return scope{Kind: "workspace", Key: strings.TrimSpace(s.workspace)}
	}
	return scope{Kind: "global", Key: ""}
}

func (s *store) resolveWriteScope(ctx context.Context, src, kind, key string) scope {
	return s.resolveReadScope(ctx, src, kind, key)
}

func (s *store) resolveSearchScopes(ctx context.Context, src, kind, key string) []scope {
	if strings.TrimSpace(kind) != "" {
		return []scope{s.scope(ctx, src, kind, key)}
	}
	var out []scope
	if p := command.PersonaFrom(ctx); p != "" {
		out = append(out, scope{Kind: "persona", Key: p})
	}
	if strings.TrimSpace(src) != "" {
		out = append(out, scope{Kind: "channel", Key: strings.TrimSpace(src)})
	}
	if strings.TrimSpace(s.workspace) != "" {
		out = append(out, scope{Kind: "workspace", Key: strings.TrimSpace(s.workspace)})
	}
	out = append(out, scope{Kind: "global", Key: ""})
	return out
}

func (s *store) scope(ctx context.Context, src, kind, key string) scope {
	return resolveScope(ctx, src, kind, key, s.workspace)
}

func resolveScope(ctx context.Context, src, kind, key, workspace string) scope {
	kind = strings.TrimSpace(kind)
	key = strings.TrimSpace(key)
	switch kind {
	case "global":
		return scope{Kind: "global", Key: ""}
	case "workspace":
		return scope{Kind: "workspace", Key: blank(key, workspace)}
	case "channel":
		return scope{Kind: "channel", Key: blank(key, src)}
	case "persona":
		return scope{Kind: "persona", Key: blank(key, command.PersonaFrom(ctx))}
	default:
		return scope{Kind: blank(kind, "custom"), Key: key}
	}
}

func normalizeScope(sc scope) scope {
	sc.Kind = strings.TrimSpace(sc.Kind)
	sc.Key = strings.TrimSpace(sc.Key)
	if sc.Kind == "" {
		sc.Kind = "global"
	}
	return sc
}

func parseScope(src, raw, workspace string) scope {
	raw = strings.TrimSpace(raw)
	switch raw {
	case "global":
		return scope{Kind: "global", Key: ""}
	case "workspace":
		return scope{Kind: "workspace", Key: workspace}
	case "channel":
		return scope{Kind: "channel", Key: src}
	default:
		kind, key, ok := strings.Cut(raw, ":")
		if !ok {
			return scope{Kind: "custom", Key: raw}
		}
		return scope{Kind: strings.TrimSpace(kind), Key: strings.TrimSpace(key)}
	}
}

func isScopeToken(s string) bool {
	s = strings.TrimSpace(s)
	return s == "global" || s == "workspace" || s == "channel" || strings.Contains(s, ":")
}

func scopeDir(root string, sc scope) string {
	sc = normalizeScope(sc)
	if sc.Key == "" {
		return filepath.Join(root, sc.Kind)
	}
	return filepath.Join(root, sc.Kind, sanitize(sc.Key))
}

func sanitize(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "_"
	}
	r := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "\n", "_", "\t", "_")
	return r.Replace(s)
}

func slug(title, fallback string) string {
	title = strings.ToLower(strings.TrimSpace(title))
	if title == "" {
		return fallback
	}
	var b strings.Builder
	lastDash := false
	for _, r := range title {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case !lastDash:
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return fallback
	}
	return out
}

func scopeText(sc scope) string {
	if sc.Key == "" {
		return sc.Kind
	}
	return sc.Kind + ":" + sc.Key
}
