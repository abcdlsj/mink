package command

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/abcdlsj/mink/tool"
)

type Router struct {
	reg   *Registry
	guard tool.Guard
}

func NewRouter(reg *Registry) *Router {
	return &Router{reg: reg}
}

func (r *Router) SetGuard(g tool.Guard) {
	r.guard = g
}

func IsCommand(input string) bool {
	return strings.HasPrefix(input, "!")
}

func (r *Router) Route(ctx context.Context, input string) (string, bool, error) {
	raw := strings.TrimPrefix(input, "!")
	parts := strings.Fields(raw)
	if len(parts) == 0 {
		return "", false, nil
	}

	name := parts[0]
	args := parts[1:]

	if cmd := r.reg.Get(name); cmd != nil {
		out, err := cmd.Run(ctx, args)
		return out, true, err
	}

	return r.shell(ctx, raw)
}

func (r *Router) shell(ctx context.Context, raw string) (string, bool, error) {
	if r.guard != nil && tool.IsDangerous(raw) {
		ok, err := r.guard.Allow(ctx, raw)
		if err != nil {
			return "", true, fmt.Errorf("guard: %w", err)
		}
		if !ok {
			return "cancelled", true, nil
		}
	}

	cmd := exec.CommandContext(ctx, "bash", "-c", raw)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), true, err
}
