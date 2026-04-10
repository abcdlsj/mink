package memory

import (
	"path/filepath"
	"strings"
)

const (
	ScopeGlobal    = "global"
	ScopeWorkspace = "workspace"
	ScopeTeam      = "team"
	ScopeAgent     = "agent"
	ScopeChannel   = "channel"
	ScopeCustom    = "custom"
)

type Scope struct {
	Kind string
	Key  string
}

func GlobalScope() Scope { return Scope{Kind: ScopeGlobal} }

func WorkspaceScope(key string) Scope {
	return Scope{Kind: ScopeWorkspace, Key: strings.TrimSpace(key)}
}

func TeamScope(key string) Scope { return Scope{Kind: ScopeTeam, Key: strings.TrimSpace(key)} }

func AgentScope(key string) Scope { return Scope{Kind: ScopeAgent, Key: strings.TrimSpace(key)} }

func ChannelScope(key string) Scope { return Scope{Kind: ScopeChannel, Key: strings.TrimSpace(key)} }

func CustomScope(kind, key string) Scope {
	kind = strings.TrimSpace(kind)
	if kind == "" {
		kind = ScopeCustom
	}
	return Scope{Kind: kind, Key: strings.TrimSpace(key)}
}

func ParseScope(raw string) Scope {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return GlobalScope()
	}
	kind, key, ok := strings.Cut(raw, ":")
	if ok {
		return normalizeScope(Scope{Kind: kind, Key: key})
	}
	return normalizeScope(Scope{Kind: raw})
}

func normalizeScope(scope Scope) Scope {
	scope.Kind = strings.TrimSpace(scope.Kind)
	scope.Key = strings.TrimSpace(scope.Key)
	if scope.Kind == "" {
		scope.Kind = ScopeGlobal
	}
	return scope
}

func (s Scope) String() string {
	s = normalizeScope(s)
	if s.Key == "" {
		return s.Kind
	}
	return s.Kind + ":" + s.Key
}

func (s Scope) Dir(root string) string {
	s = normalizeScope(s)
	if s.Key == "" {
		return filepath.Join(root, s.Kind)
	}
	return filepath.Join(root, s.Kind, sanitizePathPart(s.Key))
}

func ScopeFromPath(root, path string) Scope {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return GlobalScope()
	}
	dir := filepath.Dir(rel)
	if dir == "." || dir == "" {
		return GlobalScope()
	}
	parts := strings.Split(filepath.ToSlash(dir), "/")
	if len(parts) == 0 {
		return GlobalScope()
	}
	scope := Scope{Kind: parts[0]}
	if len(parts) > 1 {
		scope.Key = strings.Join(parts[1:], "/")
	}
	return normalizeScope(scope)
}

func sanitizePathPart(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "_"
	}
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "\n", "_", "\t", "_")
	return replacer.Replace(s)
}
