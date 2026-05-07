package command

import (
	"context"
	"strings"
)

type Router struct {
	reg *Registry
}

func NewRouter(reg *Registry) *Router {
	return &Router{reg: reg}
}

func IsCommand(input string) bool {
	input = strings.TrimSpace(input)
	return strings.HasPrefix(input, "!") || strings.HasPrefix(input, "/")
}

func (r *Router) Route(ctx context.Context, input string) (string, bool, error) {
	raw := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(input), "!/"))
	if raw == "" {
		return "", false, nil
	}
	parts := strings.Fields(raw)
	if len(parts) == 0 {
		return "", false, nil
	}
	cmd := r.reg.Get(parts[0])
	if cmd == nil {
		return "", false, nil
	}
	out, err := cmd.Run(ctx, parts[1:])
	return out, true, err
}
