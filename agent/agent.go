package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/abcdlsj/mink/event"
	"github.com/abcdlsj/mink/llm"
	"github.com/abcdlsj/mink/session"
	"github.com/abcdlsj/mink/tool"
)

type Agent struct {
	p       llm.Provider
	reg     *tool.Registry
	ext     *ExtensionManager
	sm      *session.Manager
	bus     *event.Bus
	sid     string
	maxStep int
}

func New(p llm.Provider, dir string, bus *event.Bus) *Agent {
	return &Agent{
		p:       p,
		reg:     tool.NewRegistry(),
		sm:      session.NewManager(dir),
		bus:     bus,
		maxStep: 100,
	}
}

func (a *Agent) SetExt(m *ExtensionManager) {
	a.ext = m
}

func (a *Agent) NewSession() (*session.Session, error) {
	s, err := a.sm.Create()
	if err != nil {
		return nil, err
	}
	a.sid = s.ID
	a.bus.Publish(event.Event{Type: event.SessionCreate, Data: s})
	return s, nil
}

func (a *Agent) LoadSession(id string) (*session.Session, error) {
	s, err := a.sm.Load(id)
	if err != nil {
		return nil, err
	}
	a.sid = s.ID
	a.bus.Publish(event.Event{Type: event.SessionSwitch, Data: s})
	return s, nil
}

func (a *Agent) Branch(name string) (*session.Session, error) {
	if a.sid == "" {
		return nil, fmt.Errorf("no active session")
	}
	s, err := a.sm.Branch(a.sid, name)
	if err != nil {
		return nil, err
	}
	a.bus.Publish(event.Event{Type: event.SessionBranch, Data: s})
	return s, nil
}

func (a *Agent) Run(ctx context.Context, input string) error {
	if a.sid == "" {
		if _, err := a.NewSession(); err != nil {
			return err
		}
	}

	a.bus.Publish(event.Event{Type: event.UserMessage, Data: input})
	a.sm.AddMessage(a.sid, session.Message{Role: "user", Content: input})
	a.bus.Publish(event.Event{Type: event.AgentStart})

	for i := 0; i < a.maxStep; i++ {
		done, err := a.step(ctx)
		if err != nil {
			a.bus.Publish(event.Event{Type: event.AgentError, Data: err.Error()})
			return err
		}
		if done {
			a.bus.Publish(event.Event{Type: event.AgentEnd})
			return nil
		}
	}

	a.bus.Publish(event.Event{Type: event.AgentError, Data: "max steps"})
	return fmt.Errorf("max steps")
}

func (a *Agent) step(ctx context.Context) (bool, error) {
	h, _ := a.sm.GetHistory(a.sid, -1)
	m := a.buildMsgs(h)
	t := a.getTools()

	r, err := a.p.Chat(ctx, m, t)
	if err != nil {
		return false, err
	}

	if r.Content != "" {
		a.bus.Publish(event.Event{Type: event.AssistantMessage, Data: r.Content})
		a.sm.AddMessage(a.sid, session.Message{Role: "assistant", Content: r.Content})
	}

	if len(r.ToolCalls) == 0 {
		return true, nil
	}

	for _, tc := range r.ToolCalls {
		a.bus.Publish(event.Event{
			Type: event.ToolStart,
			Data: map[string]string{"name": tc.Name, "args": string(tc.Args)},
		})

		out, err := a.execTool(ctx, tc)
		if err != nil {
			a.bus.Publish(event.Event{Type: event.ToolError, Data: map[string]string{"name": tc.Name, "error": err.Error()}})
		} else {
			a.bus.Publish(event.Event{Type: event.ToolEnd, Data: map[string]string{"name": tc.Name, "result": out}})
		}

		tr := session.ToolResult{ToolCallID: tc.ID, Content: out}
		if err != nil {
			tr.Error = err.Error()
		}
		a.sm.AddMessage(a.sid, session.Message{Role: "tool", ToolResults: []session.ToolResult{tr}})
	}

	return false, nil
}

func (a *Agent) buildMsgs(h []session.Message) []llm.Message {
	var r []llm.Message
	r = append(r, llm.Message{Role: "system", Content: a.sysPrompt()})

	for _, m := range h {
		msg := llm.Message{Role: m.Role, Content: m.Content}
		for _, tc := range m.ToolCalls {
			msg.ToolCalls = append(msg.ToolCalls, llm.ToolCall{ID: tc.ID, Name: tc.Name, Args: tc.Args})
		}
		for _, tr := range m.ToolResults {
			msg.ToolResults = append(msg.ToolResults, llm.ToolResult{ToolCallID: tr.ToolCallID, Content: tr.Content, Error: tr.Error})
		}
		r = append(r, msg)
	}
	return r
}

func (a *Agent) sysPrompt() string {
	var b strings.Builder
	b.WriteString("You are a helpful coding assistant. Available tools:\n\n")
	for _, t := range a.getTools() {
		b.WriteString(fmt.Sprintf("- %s: %s\n", t.Function.Name, t.Function.Description))
	}
	return b.String()
}

func (a *Agent) getTools() []llm.Tool {
	var r []llm.Tool
	for _, t := range a.reg.All() {
		r = append(r, llm.Tool{
			Type:     "function",
			Function: &llm.FunctionDefinition{Name: t.Name(), Description: t.Desc(), Parameters: t.Schema()},
		})
	}
	if a.ext != nil {
		for _, t := range a.ext.Tools() {
			r = append(r, llm.Tool{
				Type:     "function",
				Function: &llm.FunctionDefinition{Name: t.Name(), Description: t.Desc(), Parameters: t.Schema()},
			})
		}
	}
	return r
}

func (a *Agent) execTool(ctx context.Context, tc llm.ToolCall) (string, error) {
	if t := a.reg.Get(tc.Name); t != nil {
		return t.Run(ctx, tc.Args)
	}
	if a.ext != nil {
		if t := a.ext.Get(tc.Name); t != nil {
			return t.Run(ctx, tc.Args)
		}
	}
	return "", fmt.Errorf("unknown tool: %s", tc.Name)
}

func (a *Agent) Cmd(name string, args []string) (string, error) {
	switch name {
	case "new":
		s, err := a.NewSession()
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Created: %s", s.ID), nil
	case "branch":
		if len(args) < 1 {
			return "", fmt.Errorf("usage: /branch <name>")
		}
		s, err := a.Branch(args[0])
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Branch: %s (%s)", args[0], s.ID), nil
	case "switch":
		if len(args) < 1 {
			return "", fmt.Errorf("usage: /switch <id>")
		}
		if err := a.LoadSession(args[0]); err != nil {
			return "", err
		}
		return fmt.Sprintf("Switched: %s", args[0]), nil
	case "compact":
		sum := "Session compacted"
		if len(args) > 0 {
			sum = strings.Join(args, " ")
		}
		if err := a.sm.Compact(a.sid, sum); err != nil {
			return "", err
		}
		a.bus.Publish(event.Event{Type: event.SessionCompact})
		return "Compacted", nil
	}
	if a.ext != nil {
		return a.ext.Cmd(name, args)
	}
	return "", fmt.Errorf("unknown: %s", name)
}
