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
	tv      *toolView
	sid     string
	maxStep int
}

func New(p llm.Provider, dir string, bus *event.Bus) *Agent {
	return &Agent{
		p:       p,
		reg:     tool.NewRegistry(),
		sm:      session.NewManager(dir),
		bus:     bus,
		tv:      newToolView(),
		maxStep: 100,
	}
}

func (a *Agent) SetExt(m *ExtensionManager) { a.ext = m }

func (a *Agent) NewSession() (*session.Session, error) {
	s, err := a.sm.Create()
	if err != nil {
		return nil, err
	}
	a.sid = s.ID
	a.tv.reset()
	a.bus.Pub(event.Event{Type: event.SessionNew, Data: s})
	return s, nil
}

func (a *Agent) LoadSession(id string) (*session.Session, error) {
	s, err := a.sm.Load(id)
	if err != nil {
		return nil, err
	}
	a.sid = s.ID
	a.bus.Pub(event.Event{Type: event.SessionSwitch, Data: s})
	return s, nil
}

func (a *Agent) Branch(name string) (*session.Session, error) {
	if a.sid == "" {
		return nil, fmt.Errorf("no session")
	}
	s, err := a.sm.Branch(a.sid, name)
	if err != nil {
		return nil, err
	}
	a.bus.Pub(event.Event{Type: event.SessionBranch, Data: s})
	return s, nil
}

func (a *Agent) Run(ctx context.Context, input string) error {
	if a.sid == "" {
		if _, err := a.NewSession(); err != nil {
			return err
		}
	}

	// parse ,cmd from user input
	if cmd, ok := a.parseCmd(input); ok {
		return a.execCmd(ctx, cmd)
	}

	a.bus.Pub(event.Event{Type: event.UserMessage, Data: input})
	a.sm.AddMessage(a.sid, session.Message{Role: "user", Content: input})
	a.bus.Pub(event.Event{Type: event.AgentStart})

	for i := 0; i < a.maxStep; i++ {
		done, err := a.step(ctx)
		if err != nil {
			a.bus.Pub(event.Event{Type: event.AgentError, Data: err.Error()})
			return err
		}
		if done {
			a.bus.Pub(event.Event{Type: event.AgentEnd})
			return nil
		}
	}

	a.bus.Pub(event.Event{Type: event.AgentError, Data: "max steps"})
	return fmt.Errorf("max steps")
}

func (a *Agent) step(ctx context.Context) (bool, error) {
	h, _ := a.sm.GetHistory(a.sid, -1)
	m := a.buildMsgs(h)
	t := a.tv.tools(a.reg, a.ext)

	r, err := a.p.Chat(ctx, m, t)
	if err != nil {
		return false, err
	}

	// check for ,cmd in assistant output
	cmdOut, rest := a.extractCmd(r.Content)
	if cmdOut != "" {
		// execute ,cmd
		if cmd, ok := a.parseCmd(cmdOut); ok {
			if err := a.execCmd(ctx, cmd); err != nil {
				// failed command goes back to model as context
				a.sm.AddMessage(a.sid, session.Message{
					Role:    "system",
					Content: fmt.Sprintf("<cmd name=\"%s\" status=\"error\">%s</cmd>", cmd.name, err.Error()),
				})
				return false, nil
			}
			// success - add result as context
			a.sm.AddMessage(a.sid, session.Message{
				Role:    "system",
				Content: fmt.Sprintf("<cmd name=\"%s\" status=\"ok\">executed</cmd>", cmd.name),
			})
		}
	}

	if rest != "" {
		a.bus.Pub(event.Event{Type: event.AssistantMsg, Data: rest})
		a.sm.AddMessage(a.sid, session.Message{Role: "assistant", Content: rest})
	}

	if len(r.ToolCalls) == 0 && cmdOut == "" {
		return true, nil
	}

	// execute tool calls
	for _, tc := range r.ToolCalls {
		// expand tool view for next iteration
		a.tv.expand(tc.Name)

		a.bus.Pub(event.Event{
			Type: event.ToolStart,
			Data: map[string]string{"name": tc.Name, "args": string(tc.Args)},
		})

		out, err := a.execTool(ctx, tc)

		if err != nil {
			a.bus.Pub(event.Event{
				Type: event.ToolError,
				Data: map[string]string{"name": tc.Name, "error": err.Error()},
			})
			// failed tool - add structured error context
			a.sm.AddMessage(a.sid, session.Message{
				Role: "tool",
				ToolResults: []session.ToolResult{{
					ToolCallID: tc.ID,
					Content:    "",
					Error:      err.Error(),
				}},
			})
		} else {
			a.bus.Pub(event.Event{
				Type: event.ToolEnd,
				Data: map[string]string{"name": tc.Name, "result": out},
			})
			a.sm.AddMessage(a.sid, session.Message{
				Role: "tool",
				ToolResults: []session.ToolResult{{
					ToolCallID: tc.ID,
					Content:    out,
				}},
			})
		}
	}

	return false, nil
}

func (a *Agent) buildMsgs(h []session.Message) []llm.Message {
	var r []llm.Message
	r = append(r, llm.Message{Role: "system", Content: a.sysPrompt()})

	for _, m := range h {
		msg := llm.Message{Role: m.Role, Content: m.Content}
		for _, tc := range m.ToolCalls {
			msg.ToolCalls = append(msg.ToolCalls, llm.ToolCall{
				ID:   tc.ID,
				Name: tc.Name,
				Args: tc.Args,
			})
		}
		for _, tr := range m.ToolResults {
			msg.ToolResults = append(msg.ToolResults, llm.ToolResult{
				ToolCallID: tr.ToolCallID,
				Content:    tr.Content,
				Error:      tr.Error,
			})
		}
		r = append(r, msg)
	}
	return r
}

func (a *Agent) sysPrompt() string {
	var b strings.Builder
	b.WriteString("You are a helpful coding assistant.\n\n")
	b.WriteString("Available tools (use $name to expand details):\n")
	for _, t := range a.tv.compact(a.reg, a.ext) {
		b.WriteString(fmt.Sprintf("- %s: %s\n", t.name, t.desc))
	}
	b.WriteString("\nYou can also use shell commands: ,bash <cmd>")
	return b.String()
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
	return "", fmt.Errorf("unknown: %s", tc.Name)
}

type cmd struct {
	name string
	args []string
}

func (a *Agent) parseCmd(s string) (cmd, bool) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, ",") {
		return cmd{}, false
	}
	parts := strings.Fields(s[1:])
	if len(parts) == 0 {
		return cmd{}, false
	}
	return cmd{name: parts[0], args: parts[1:]}, true
}

func (a *Agent) extractCmd(s string) (string, string) {
	lines := strings.Split(s, "\n")
	var cmdLines []string
	var otherLines []string
	inCmd := false

	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, ",") {
			cmdLines = append(cmdLines, trim)
			inCmd = true
		} else if inCmd && trim == "" {
			continue
		} else {
			otherLines = append(otherLines, line)
			inCmd = false
		}
	}

	return strings.Join(cmdLines, "\n"), strings.Join(otherLines, "\n")
}

func (a *Agent) execCmd(ctx context.Context, c cmd) error {
	a.bus.Pub(event.Event{Type: event.ToolStart, Data: map[string]string{"name": c.name, "args": strings.Join(c.args, " ")}})

	var out string
	var err error

	switch c.name {
	case "bash":
		if len(c.args) == 0 {
			err = fmt.Errorf("no cmd")
			break
		}
		t := a.reg.Get("bash")
		args, _ := json.Marshal(map[string]string{"cmd": strings.Join(c.args, " ")})
		out, err = t.Run(ctx, args)
	default:
		// try extension command
		if a.ext != nil {
			out, err = a.ext.Cmd(c.name, c.args)
		} else {
			err = fmt.Errorf("unknown: %s", c.name)
		}
	}

	if err != nil {
		a.bus.Pub(event.Event{Type: event.ToolError, Data: map[string]string{"name": c.name, "error": err.Error()}})
		return err
	}

	a.bus.Pub(event.Event{Type: event.ToolEnd, Data: map[string]string{"name": c.name, "result": out}})
	return nil
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
		sum := "compacted"
		if len(args) > 0 {
			sum = strings.Join(args, " ")
		}
		if err := a.sm.Compact(a.sid, sum); err != nil {
			return "", err
		}
		a.bus.Pub(event.Event{Type: event.SessionCompact})
		return "Compacted", nil
	}
	if a.ext != nil {
		return a.ext.Cmd(name, args)
	}
	return "", fmt.Errorf("unknown: %s", name)
}
