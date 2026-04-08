package command

import "context"

type FuncCommand struct {
	name string
	desc string
	run  func(context.Context, []string) (string, error)
}

func NewFuncCmd(name, desc string, run func(context.Context, []string) (string, error)) Command {
	return &FuncCommand{name: name, desc: desc, run: run}
}

func (c *FuncCommand) Name() string { return c.name }

func (c *FuncCommand) Desc() string { return c.desc }

func (c *FuncCommand) Run(ctx context.Context, args []string) (string, error) {
	return c.run(ctx, args)
}
