package command

import (
	"context"
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

func (r *Registry) Register(c Command) {
	r.cmds[c.Name()] = c
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
