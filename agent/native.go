package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/llm"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/session"
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
	ProjectContext       string
	SoulPath             string
	PreferencesPath      string
	SkillCards           []string
	ChildEnv             []string
	Prompt               string
	TelegramMentionMode  string
	TelegramSessionScope string
	Persona              *Persona
}

type Persona struct {
	ID          string
	Display     string
	Description string
	SoulPath    string
}

type Turn struct {
	Source                string
	Input                 string
	Attachments           []msg.Attachment
	Session               *session.Session
	Bus                   *bus.Bus
	SpaceID               string
	ParentMessageID       string
	AgentID               string
	StreamID              string
	CollaborationBrief    string
	IncludeHistory        bool
	DisableExternalResume bool
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
	return &Native{engine: &engine{env: env}}, nil
}

func (n *Native) Run(ctx context.Context, t *Turn) error {
	if n == nil || n.engine == nil {
		return fmt.Errorf("native runtime is not initialized")
	}
	return n.engine.run(ctx, t)
}
