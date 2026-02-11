package cmd

import (
	"context"
	"strings"
)

type Guard interface {
	Allow(ctx context.Context, cmd string) (bool, error)
}

type ctxKey string

const SourceKey ctxKey = "guard:source"

func WithSource(ctx context.Context, src string) context.Context {
	return context.WithValue(ctx, SourceKey, src)
}

func SourceFrom(ctx context.Context) string {
	if v, ok := ctx.Value(SourceKey).(string); ok {
		return v
	}
	return ""
}

type GuardMux struct {
	guards map[string]Guard
}

func NewGuardMux() *GuardMux {
	return &GuardMux{guards: make(map[string]Guard)}
}

func (m *GuardMux) Register(prefix string, g Guard) {
	m.guards[prefix] = g
}

func (m *GuardMux) Allow(ctx context.Context, cmd string) (bool, error) {
	src := SourceFrom(ctx)
	for prefix, g := range m.guards {
		if strings.HasPrefix(src, prefix) {
			return g.Allow(ctx, cmd)
		}
	}
	return true, nil
}

var dangerousPrefixes = []string{
	"rm ", "rm\t",
	"mv ", "mv\t",
	"cp ", "cp\t",
	"git push", "git reset", "git checkout .",
	"docker run", "docker rm", "docker stop",
	"kubectl apply", "kubectl delete",
}

func IsDangerous(raw string) bool {
	cmd := strings.TrimSpace(raw)
	for _, p := range dangerousPrefixes {
		if strings.HasPrefix(cmd, p) {
			return true
		}
	}
	return false
}
