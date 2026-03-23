package command

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/config"
	"github.com/abcdlsj/mink/msg"
	"github.com/abcdlsj/mink/session"
	"github.com/abcdlsj/mink/tool"
)

type helpCmd struct {
	reg *Registry
}

func NewHelpCmd(reg *Registry) Command { return &helpCmd{reg: reg} }

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

func NewToolsCmd(all func() []tool.Tool) Command { return &toolsCmd{all: all} }

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
	sm  *session.Manager
	dir string
}

func NewReplayCmd(sm *session.Manager, dir string) Command {
	return &replayCmd{sm: sm, dir: dir}
}

func (c *replayCmd) Name() string { return "replay" }
func (c *replayCmd) Desc() string { return "replay current session runlog (!replay [count])" }

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

	path := filepath.Join(c.dir, id+".log.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "no replay log for current session", nil
		}
		return "", err
	}

	raw := strings.TrimSpace(string(data))
	if raw == "" {
		return "no replay log for current session", nil
	}
	lines := strings.Split(raw, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}

	type replayEvent struct {
		Timestamp time.Time      `json:"timestamp"`
		Type      string         `json:"type"`
		Level     string         `json:"level"`
		StepNum   *int           `json:"step_num"`
		Data      map[string]any `json:"data"`
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Replay session %s (last %d events)\n", id, len(lines))
	for _, line := range lines {
		var e replayEvent
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			fmt.Fprintf(&b, "%s\n", line)
			continue
		}
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
	return strings.TrimSpace(b.String()), nil
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

type sessionCmd struct {
	sm    *session.Manager
	reset interface{ InvalidateSource(string) }
}

func NewSessionCmd(sm *session.Manager, reset interface{ InvalidateSource(string) }) Command {
	return &sessionCmd{sm: sm, reset: reset}
}

func (c *sessionCmd) Name() string { return "session" }
func (c *sessionCmd) Desc() string { return "session management (list/current/new/switch/fork)" }

func (c *sessionCmd) Run(ctx context.Context, args []string) (string, error) {
	src := bus.SourceFrom(ctx)
	if src == "" {
		src = bus.AddrPlatformCLI
	}
	if len(args) == 0 {
		return "usage: !session [list|current|new|switch <id>|fork]", nil
	}

	switch args[0] {
	case "list":
		ids, err := c.sm.List()
		if err != nil {
			return "", err
		}
		if len(ids) == 0 {
			return "no sessions", nil
		}
		var b strings.Builder
		b.WriteString("Sessions:\n")
		current, _ := c.sm.CurrentID(src)
		for _, id := range ids {
			mark := "  "
			if id == current {
				mark = "* "
			}
			fmt.Fprintf(&b, "%s%s\n", mark, id)
		}
		return b.String(), nil
	case "current":
		id, ok := c.sm.CurrentID(src)
		if !ok {
			return "no current session for source", nil
		}
		s, err := c.sm.Get(id)
		if err != nil {
			return "", err
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Current session: %s\n", id)
		fmt.Fprintf(&b, "Source: %s\n", src)
		fmt.Fprintf(&b, "Entries: %d\n", s.EntryCount())
		fmt.Fprintf(&b, "Anchors: %d", len(s.Anchors()))
		if p := s.Provenance(); p != nil {
			fmt.Fprintf(&b, "\nParent: %s\nFork point: %d", p.ParentSessionID, p.ForkEntryCount)
		}
		return b.String(), nil
	case "new":
		s, err := c.sm.ResetSource(src)
		if err != nil {
			return "", err
		}
		c.invalidate(src)
		return fmt.Sprintf("switched to new session: %s", s.ID()), nil
	case "switch":
		if len(args) < 2 {
			return "usage: !session switch <id>", nil
		}
		if err := c.sm.RestoreSource(src, args[1]); err != nil {
			return "", err
		}
		c.invalidate(src)
		return fmt.Sprintf("switched to session: %s", args[1]), nil
	case "fork":
		s, err := c.sm.ForkSource(src)
		if err != nil {
			return "", err
		}
		c.invalidate(src)
		return fmt.Sprintf("forked current session: %s", s.ID()), nil
	default:
		return "usage: !session [list|current|new|switch <id>|fork]", nil
	}
}

func (c *sessionCmd) invalidate(src string) {
	if c.reset != nil {
		c.reset.InvalidateSource(src)
	}
}

type compactCmd struct {
	b *bus.Bus
}

func NewCompactCmd(b *bus.Bus) Command { return &compactCmd{b: b} }

func (c *compactCmd) Name() string { return "compact" }
func (c *compactCmd) Desc() string { return "compact current conversation context" }

func (c *compactCmd) Run(ctx context.Context, args []string) (string, error) {
	src := bus.SourceFrom(ctx)
	if src == "" {
		src = bus.AddrPlatformCLI
	}
	note := strings.TrimSpace(strings.Join(args, " "))

	if err := c.b.Pub(bus.Msg{
		Type:    bus.TypeSessionCompact,
		From:    src,
		To:      bus.AddrAgentMain,
		Payload: note,
	}); err != nil {
		return "", err
	}

	if note == "" {
		return "compact requested", nil
	}
	return "compact requested with note", nil
}

type tokensCmd struct {
	usage func(src string) (msg.TokenUsage, bool)
}

func NewTokensCmd(usage func(src string) (msg.TokenUsage, bool)) Command {
	return &tokensCmd{usage: usage}
}

func (c *tokensCmd) Name() string { return "tokens" }
func (c *tokensCmd) Desc() string { return "show estimated token usage for current session" }

func (c *tokensCmd) Run(ctx context.Context, args []string) (string, error) {
	src := bus.SourceFrom(ctx)
	if src == "" {
		src = bus.AddrPlatformCLI
	}

	u, ok := c.usage(src)
	if !ok {
		return "no active session for current source", nil
	}

	return fmt.Sprintf(
		"Estimated tokens\n  total: %d\n  messages: %d\n  input(user): %d\n  output(assistant): %d\n  system: %d\n  tool: %d\n  source: %s",
		u.Total,
		u.Messages,
		u.Input,
		u.Output,
		u.System,
		u.Tool,
		u.Source,
	), nil
}

type ModelInfo struct {
	Models map[string]config.ModelConfig
	Active string
}

type modelsCmd struct {
	info func() ModelInfo
}

func NewModelsCmd(info func() ModelInfo) Command { return &modelsCmd{info: info} }

func (c *modelsCmd) Name() string { return "models" }
func (c *modelsCmd) Desc() string { return "list available models" }

func (c *modelsCmd) Run(ctx context.Context, args []string) (string, error) {
	info := c.info()
	if len(info.Models) == 0 {
		return "no models configured", nil
	}

	names := make([]string, 0, len(info.Models))
	for k := range info.Models {
		names = append(names, k)
	}
	sort.Strings(names)

	var b strings.Builder
	b.WriteString("Models:\n")
	for _, name := range names {
		mc := info.Models[name]
		marker := "  "
		if name == info.Active {
			marker = "* "
		}
		fmt.Fprintf(&b, "  %s%s (%s/%s)\n", marker, name, mc.Provider, mc.Model)
	}
	return b.String(), nil
}

type modelCmd struct {
	switchFn func(name string) error
}

func NewModelCmd(switchFn func(name string) error) Command { return &modelCmd{switchFn: switchFn} }

func (c *modelCmd) Name() string { return "model" }
func (c *modelCmd) Desc() string { return "switch model (!model <name>)" }

func (c *modelCmd) Run(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return "usage: !model <name>", nil
	}
	name := args[0]
	if err := c.switchFn(name); err != nil {
		return "", err
	}
	return fmt.Sprintf("switched to %s", name), nil
}
