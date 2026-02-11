package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/cmd"
	"github.com/abcdlsj/mink/hook"
	"github.com/abcdlsj/mink/llm"
	"github.com/abcdlsj/mink/session"
	"github.com/abcdlsj/mink/tool"
)

var fenceRe = regexp.MustCompile("^```")

func (c *Core) step(ctx context.Context, src, sid string) (bool, error) {
	h, _ := c.sm.GetHistory(sid, -1)
	msgs := c.buildMsgs(h)

	r, err := c.p.Chat(ctx, msgs, tools(c.reg))
	if err != nil {
		return false, err
	}

	if len(r.ToolCalls) > 0 || r.Content != "" {
		var tcs []session.ToolCall
		for _, tc := range r.ToolCalls {
			tcs = append(tcs, session.ToolCall{
				ID:   tc.ID,
				Name: tc.Name,
				Args: tc.Args,
			})
		}
		c.sm.AddMessage(sid, session.Message{
			Role:             "assistant",
			Content:          r.Content,
			ReasoningContent: r.ReasoningContent,
			ToolCalls:        tcs,
		})
	}

	if r.Content != "" {
		if c.router != nil {
			if cmdResult := c.detectAndExecCommands(ctx, src, sid, r.Content); cmdResult != "" {
				return false, nil
			}
		}

		c.hooks.Trigger(ctx, hook.BeforeAssist, r.Content)
		c.bus.Pub(bus.Msg{
			Type:    bus.TypeAssistant,
			From:    c.id,
			To:      src,
			Payload: r.Content,
		})
		c.hooks.Trigger(ctx, hook.AfterAssist, r.Content)
	}

	if len(r.ToolCalls) == 0 {
		return true, nil
	}

	// 并行执行 tool calls
	results := make([]session.ToolResult, len(r.ToolCalls))
	var wg sync.WaitGroup

	for i, tc := range r.ToolCalls {
		c.hooks.Trigger(ctx, hook.BeforeTool, tc)
		c.bus.Pub(bus.Msg{
			Type:    bus.TypeToolCall,
			From:    c.id,
			To:      src,
			Payload: fmtToolCall(tc.Name, tc.Args),
		})

		wg.Add(1)
		go func(i int, tc llm.ToolCall) {
			defer wg.Done()
			out, err := c.execTool(ctx, tc)

			tr := session.ToolResult{ToolCallID: tc.ID, Content: out}
			if err != nil {
				tr.Error = err.Error()
				c.bus.Pub(bus.Msg{
					Type:    bus.TypeToolError,
					From:    c.id,
					To:      src,
					Payload: err.Error(),
				})
			} else {
				c.bus.Pub(bus.Msg{
					Type:    bus.TypeToolResult,
					From:    c.id,
					To:      src,
					Payload: out,
				})
			}
			c.hooks.Trigger(ctx, hook.AfterTool, tr)
			results[i] = tr
		}(i, tc)
	}

	wg.Wait()
	for _, tr := range results {
		c.sm.AddMessage(sid, session.Message{Role: "tool", ToolResults: []session.ToolResult{tr}})
	}

	return false, nil
}

func (c *Core) buildMsgs(h []session.Message) []llm.Message {
	var r []llm.Message
	r = append(r, llm.Message{Role: "system", Content: c.prompt()})

	for _, m := range h {
		msg := llm.Message{
			Role:             m.Role,
			Content:          m.Content,
			ReasoningContent: m.ReasoningContent,
		}
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

func (c *Core) prompt() string {
	var b strings.Builder
	b.WriteString("You are a helpful coding assistant.\n\n")

	if c.customPrompt != "" {
		b.WriteString(c.customPrompt)
		b.WriteString("\n\n")
	}

	b.WriteString("Available tools:\n")
	for _, t := range c.reg.All() {
		fmt.Fprintf(&b, "- %s: %s\n", t.Name(), t.Desc())
	}

	if c.reg.Get("spawn") != nil {
		b.WriteString("\n## Multi-Agent Collaboration\n")
		b.WriteString("Use `spawn` to delegate subtasks to new agents. Good for:\n")
		b.WriteString("- Parallel work: spawn multiple agents to handle independent tasks\n")
		b.WriteString("- Complex tasks: break down into smaller subtasks\n")
		b.WriteString("- Focused work: let each agent focus on one specific thing\n")
		b.WriteString("Example: spawn({\"task\": \"analyze error handling in cmd/\", \"share_context\": false})\n")
	}

	if c.reg.Get("background") != nil {
		b.WriteString("\n## Background Tasks\n")
		b.WriteString("Use `background` for long-running commands. Returns immediately with task_id.\n")
		b.WriteString("When task completes, you'll receive a notification with the result.\n")
		b.WriteString("Good for: downloads, builds, tests, or any command that takes time.\n")
		b.WriteString("Example: background({\"cmd\": \"go build ./...\", \"cwd\": \"/path/to/project\"})\n")
	}

	if c.router != nil {
		b.WriteString("\n## Commands (PREFERRED over bash tool)\n")
		b.WriteString("Execute shell commands in code blocks with `!` prefix:\n")
		b.WriteString("```bash\n!ls -la\n!git status\n```\n")
		b.WriteString("IMPORTANT: Always use `!command` format instead of bash tool.\n")
		b.WriteString("The `!` prefix is REQUIRED. Without it, commands won't execute.\n")
	}

	return b.String()
}

func (c *Core) execTool(ctx context.Context, tc llm.ToolCall) (string, error) {
	if t := c.reg.Get(tc.Name); t != nil {
		return t.Run(ctx, tc.Args)
	}
	return "", fmt.Errorf("unknown: %s", tc.Name)
}

func (c *Core) detectAndExecCommands(ctx context.Context, src, sid, content string) string {
	cmds := c.parseCommands(content)
	if len(cmds) == 0 {
		return ""
	}

	ctx = cmd.WithSource(ctx, src)

	var results []string
	for _, raw := range cmds {
		out, ok, err := c.router.Route(ctx, raw)
		if !ok {
			continue
		}

		c.bus.Pub(bus.Msg{
			Type:    bus.TypeCommand,
			From:    c.id,
			To:      src,
			Payload: raw,
		})

		status := "ok"
		if err != nil {
			status = "error"
			out = err.Error()
			c.bus.Pub(bus.Msg{
				Type:    bus.TypeCommandError,
				From:    c.id,
				To:      src,
				Payload: out,
			})
		} else {
			c.bus.Pub(bus.Msg{
				Type:    bus.TypeCommandOK,
				From:    c.id,
				To:      src,
				Payload: out,
			})
		}
		results = append(results, fmt.Sprintf("<command cmd=%q status=%q>\n%s\n</command>", raw, status, out))
	}

	if len(results) == 0 {
		return ""
	}

	feedback := strings.Join(results, "\n")
	c.sm.AddMessage(sid, session.Message{Role: "user", Content: feedback})
	return feedback
}

func (c *Core) parseCommands(content string) []string {
	var cmds []string
	lines := strings.Split(content, "\n")
	inFence := false

	for _, line := range lines {
		stripped := strings.TrimSpace(line)
		if fenceRe.MatchString(stripped) {
			inFence = !inFence
			continue
		}

		if inFence && cmd.IsCommand(stripped) {
			cmds = append(cmds, strings.TrimPrefix(stripped, "!"))
		}
	}
	return cmds
}

func fmtToolCall(name string, args json.RawMessage) string {
	var buf bytes.Buffer
	buf.WriteString(name)
	buf.WriteByte(' ')
	json.Compact(&buf, args)
	return buf.String()
}

func tools(reg *tool.Registry) []llm.Tool {
	var r []llm.Tool
	for _, t := range reg.All() {
		r = append(r, llm.Tool{
			Type: "function",
			Function: &llm.FunctionDef{
				Name:        t.Name(),
				Description: t.Desc(),
				Parameters:  t.Schema(),
			},
		})
	}
	return r
}
