package builtin

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/command"
	rtsqlite "github.com/abcdlsj/mink/runtime/store/sqlite"
	"github.com/abcdlsj/mink/session"
	"github.com/abcdlsj/mink/tool"
)

type helpCmd struct {
	reg *command.Registry
}

func NewHelpCmd(reg *command.Registry) command.Command { return &helpCmd{reg: reg} }

func (c *helpCmd) Name() string { return "help" }
func (c *helpCmd) Desc() string { return "show available commands" }

func (c *helpCmd) Run(ctx context.Context, args []string) (string, error) {
	var b strings.Builder
	b.WriteString("Commands:\n")
	for _, cmd := range c.reg.All() {
		fmt.Fprintf(&b, "  !%s - %s\n", cmd.Name(), cmd.Desc())
	}
	b.WriteString("\nShell commands: !<cmd> (e.g., !ls -la)")
	return b.String(), nil
}

type toolsCmd struct {
	all func() []tool.Tool
}

func NewToolsCmd(all func() []tool.Tool) command.Command { return &toolsCmd{all: all} }

func (c *toolsCmd) Name() string { return "tools" }
func (c *toolsCmd) Desc() string { return "list available tools" }

func (c *toolsCmd) Run(ctx context.Context, args []string) (string, error) {
	var b strings.Builder
	b.WriteString("Tools:\n")
	for _, t := range c.all() {
		fmt.Fprintf(&b, "  %s - %s\n", t.Name(), t.Desc())
	}
	return b.String(), nil
}

type replayCmd struct {
	sm *session.Manager
	rt *rtsqlite.DB
}

func NewReplayCmd(sm *session.Manager, rt *rtsqlite.DB) command.Command {
	return &replayCmd{sm: sm, rt: rt}
}

func (c *replayCmd) Name() string { return "replay" }
func (c *replayCmd) Desc() string { return "replay current session activity (!replay [count])" }

func (c *replayCmd) Run(ctx context.Context, args []string) (string, error) {
	src := bus.SourceFrom(ctx)
	if src == "" {
		src = bus.AddrPlatformCLI
	}

	id, ok := c.sm.CurrentID(src)
	if !ok || id == "" {
		return "no current session for source", nil
	}

	n := 30
	if len(args) > 0 {
		v, err := strconv.Atoi(strings.TrimSpace(args[0]))
		if err != nil || v <= 0 {
			return "usage: !replay [count]", nil
		}
		if v > 200 {
			v = 200
		}
		n = v
	}

	if c.rt == nil {
		return "runtime database unavailable", nil
	}
	events, err := c.rt.ReplayEventsForSession(ctx, id, n)
	if err != nil {
		return "", err
	}
	if len(events) == 0 {
		return "no replay activity for current session", nil
	}
	return renderReplay(id, events), nil
}

func renderReplay(id string, events []rtsqlite.ReplayEvent) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Replay session %s (last %d events)\n", id, len(events))
	for _, e := range events {
		ts := e.Timestamp.Format("15:04:05")
		if ts == "00:00:00" && !e.Timestamp.IsZero() {
			ts = e.Timestamp.Format(time.RFC3339)
		}
		step := ""
		if e.StepNum != nil {
			step = fmt.Sprintf(" step=%d", *e.StepNum)
		}
		extra := replayExtra(e.Type, e.Data)
		if extra != "" {
			extra = " " + extra
		}
		fmt.Fprintf(&b, "%s [%s] %s%s%s\n", ts, e.Level, e.Type, step, extra)
	}
	return strings.TrimSpace(b.String())
}

func replayExtra(t string, data map[string]any) string {
	if len(data) == 0 {
		return ""
	}
	switch t {
	case "user_input":
		if v, ok := data["input"].(string); ok {
			return replayTrim(v, 80)
		}
	case "agent_output":
		if v, ok := data["content"].(string); ok {
			return replayTrim(v, 80)
		}
	case "tool_call":
		name, _ := data["name"].(string)
		if name != "" {
			return name
		}
	case "tool_end":
		name, _ := data["name"].(string)
		if err, ok := data["error"].(string); ok && err != "" {
			return name + " error=" + replayTrim(err, 80)
		}
		if name != "" {
			return name
		}
	case "llm_error":
		if err, ok := data["error"].(string); ok {
			return replayTrim(err, 80)
		}
	}
	return ""
}

func replayTrim(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	if n <= 1 {
		return "…"
	}
	return string(r[:n-1]) + "…"
}
