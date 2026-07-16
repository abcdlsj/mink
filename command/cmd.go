package command

import (
	"context"
	"fmt"
	"sort"
)

type Command interface {
	Name() string
	Desc() string
	Run(ctx context.Context, args []string) (string, error)
}

type Registry struct {
	cmds map[string]Command
}

func NewRegistry() *Registry {
	return &Registry{cmds: make(map[string]Command)}
}

// Register adds a command under its Name(). Duplicate names are rejected rather
// than silently overwritten: a name collision means two implementations claim
// the same verb, and whichever registers last would win by load order alone —
// exactly the ambiguity Phase 1 removes. Callers converge on a single
// authoritative implementation instead of relying on registration order.
func (r *Registry) Register(c Command) error {
	name := c.Name()
	if _, exists := r.cmds[name]; exists {
		return fmt.Errorf("command %q already registered", name)
	}
	r.cmds[name] = c
	return nil
}

func (r *Registry) Get(name string) Command {
	return r.cmds[name]
}

func (r *Registry) All() []Command {
	keys := make([]string, 0, len(r.cmds))
	for k := range r.cmds {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	cmds := make([]Command, 0, len(keys))
	for _, k := range keys {
		cmds = append(cmds, r.cmds[k])
	}
	return cmds
}
