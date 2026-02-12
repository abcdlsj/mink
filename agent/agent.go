package agent

import (
	"context"
	"fmt"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/cmd"
	"github.com/abcdlsj/mink/hook"
	"github.com/abcdlsj/mink/llm"
	"github.com/abcdlsj/mink/msg"
	"github.com/abcdlsj/mink/session"
	"github.com/abcdlsj/mink/tool"
)

type Agent struct {
	id      string
	p       llm.Provider
	reg     *tool.Registry
	session *session.Session
	bus     *bus.Bus
	hooks   *hook.Manager
	router  *cmd.Router
	prompt  string
}

type Option func(*Agent)

func WithHooks(h *hook.Manager) Option   { return func(a *Agent) { a.hooks = h } }
func WithRouter(r *cmd.Router) Option    { return func(a *Agent) { a.router = r } }
func WithPrompt(p string) Option         { return func(a *Agent) { a.prompt = p } }
func WithBus(b *bus.Bus) Option          { return func(a *Agent) { a.bus = b } }
func WithRegistry(r *tool.Registry) Option { return func(a *Agent) { a.reg = r } }

func New(id string, p llm.Provider, s *session.Session, opts ...Option) *Agent {
	a := &Agent{
		id:      id,
		p:       p,
		session: s,
		reg:     tool.NewRegistry(),
		hooks:   hook.NewManager(),
	}
	for _, opt := range opts {
		opt(a)
	}
	if a.bus != nil {
		a.reg.Register(tool.NewSpawn(a.bus, id))
		a.reg.Register(tool.NewBackground(a.bus, id))
	}
	return a
}

func (a *Agent) ID() string              { return a.id }
func (a *Agent) Session() *session.Session { return a.session }
func (a *Agent) Tools() *tool.Registry   { return a.reg }

func (a *Agent) Run(ctx context.Context, src, input string) error {
	a.session.Add(msg.Message{Role: "user", Content: input})

	for i := 0; i < 100; i++ {
		done, err := a.step(ctx, src)
		if err != nil {
			return err
		}
		if done {
			return a.session.Flush()
		}
	}
	return fmt.Errorf("max steps")
}
