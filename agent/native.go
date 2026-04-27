package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/llm"
	"github.com/abcdlsj/mink/session"
)

type Runtime interface {
	Run(context.Context, *Turn) error
}

type RuntimeFactory func(*RuntimeEnv) (Runtime, error)

type ToolRunner interface {
	Definitions() []llm.Tool
	Run(context.Context, string, json.RawMessage) (string, error)
}

type RuntimeEnv struct {
	Provider             llm.Provider
	Tools                ToolRunner
	Workspace            string
	SoulPath             string
	Prompt               string
	TelegramMentionMode  string
	TelegramSessionScope string
	MaxSteps             int
}

type Turn struct {
	Source  string
	Input   string
	Session *session.Session
	Bus     *bus.Bus
}

type Native struct {
	engine *engine
}

func NewNative(env *RuntimeEnv) (Runtime, error) {
	if env == nil || env.Provider == nil {
		return nil, fmt.Errorf("native runtime requires provider")
	}
	if env.Tools == nil {
		return nil, fmt.Errorf("native runtime requires tools")
	}
	if env.MaxSteps <= 0 {
		env.MaxSteps = 8
	}
	return &Native{engine: &engine{env: env}}, nil
}

func (n *Native) Run(ctx context.Context, t *Turn) error {
	if n == nil || n.engine == nil {
		return fmt.Errorf("native runtime is not initialized")
	}
	return n.engine.run(ctx, t)
}
