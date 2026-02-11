package cmd

import "context"

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
	var cmds []Command
	for _, c := range r.cmds {
		cmds = append(cmds, c)
	}
	return cmds
}
