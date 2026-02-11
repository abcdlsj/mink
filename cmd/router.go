package cmd

import (
	"context"
	"os/exec"
	"strings"
)

type Router struct {
	reg *Registry
}

func NewRouter(reg *Registry) *Router {
	return &Router{reg: reg}
}

func IsCommand(input string) bool {
	return strings.HasPrefix(input, "!")
}

func (r *Router) Route(ctx context.Context, input string) (string, bool, error) {
	if !IsCommand(input) {
		return "", false, nil
	}

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
	cmd := exec.CommandContext(ctx, "bash", "-c", raw)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), true, err
}
