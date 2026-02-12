package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/command"
	"github.com/abcdlsj/mink/hook"
	"github.com/abcdlsj/mink/llm"
	"github.com/abcdlsj/mink/msg"
	"github.com/abcdlsj/mink/tool"
)

var fenceRe = regexp.MustCompile("^```")

func (a *Agent) step(ctx context.Context, src string) (bool, error) {
	msgs := a.session.Messages()
	sysMsgs := []msg.Message{{Role: "system", Content: a.buildPrompt()}}
	allMsgs := append(sysMsgs, msgs...)

	var r *llm.Response
	var err error

	llmTimeout := time.Duration(a.cfg.Timeout.LLM) * time.Second
	llmCtx := ctx
	if llmTimeout > 0 {
		var cancel context.CancelFunc
		llmCtx, cancel = context.WithTimeout(ctx, llmTimeout)
		defer cancel()
	}

	if a.stream {
		r, err = a.stepStream(llmCtx, src, allMsgs)
	} else {
		r, err = a.p.Chat(llmCtx, allMsgs, tools(a.reg))
	}
	if err != nil {
		return false, err
	}
	a.updateTokenBaseline(msgs, sysMsgs, r.Usage)

	if len(r.ToolCalls) > 0 || r.Content != "" {
		a.session.Add(msg.Message{
			Role:             "assistant",
			Content:          r.Content,
			ReasoningContent: r.ReasoningContent,
			ToolCalls:        r.ToolCalls,
		})
	}

	if r.Content != "" {
		if a.router != nil {
			if cmdResult := a.detectAndExecCommands(ctx, src, r.Content); cmdResult != "" {
				return false, nil
			}
		}

		a.hooks.Trigger(ctx, hook.BeforeAssist, r.Content)
		if a.bus != nil && !a.stream {
			_ = a.bus.Pub(bus.Msg{
				Type:    bus.TypeAssistant,
				From:    a.id,
				To:      src,
				Payload: r.Content,
			})
		}
		a.hooks.Trigger(ctx, hook.AfterAssist, r.Content)
	}

	if len(r.ToolCalls) == 0 {
		return true, nil
	}

	results := make([]msg.ToolResult, len(r.ToolCalls))
	var wg sync.WaitGroup

	for i, tc := range r.ToolCalls {
		a.hooks.Trigger(ctx, hook.BeforeTool, tc)
		if a.bus != nil {
			_ = a.bus.Pub(bus.Msg{
				Type:    bus.TypeToolCall,
				From:    a.id,
				To:      src,
				Payload: fmtToolCall(tc.Name, tc.Args),
			})
		}

		wg.Add(1)
		go func(i int, tc msg.ToolCall) {
			defer wg.Done()
			out, toolErr := a.execTool(ctx, tc)

			tr := msg.ToolResult{ToolCallID: tc.ID, Content: out}
			if toolErr != nil {
				tr.Content = tool.FormatErrorForLLM(tc.Name, toolErr)
				tr.Error = toolErr.Error()
				if a.bus != nil {
					_ = a.bus.Pub(bus.Msg{
						Type:    bus.TypeToolError,
						From:    a.id,
						To:      src,
						Payload: toolErr.Error(),
					})
				}
			} else {
				if a.bus != nil {
					_ = a.bus.Pub(bus.Msg{
						Type:    bus.TypeToolResult,
						From:    a.id,
						To:      src,
						Payload: out,
					})
				}
			}
			a.hooks.Trigger(ctx, hook.AfterTool, tr)
			results[i] = tr
		}(i, tc)
	}

	wg.Wait()
	for _, tr := range results {
		a.session.Add(msg.Message{Role: "tool", ToolResults: []msg.ToolResult{tr}})
	}

	return false, nil
}

func (a *Agent) stepStream(ctx context.Context, src string, allMsgs []msg.Message) (*llm.Response, error) {
	ch, err := a.p.ChatStream(ctx, allMsgs, tools(a.reg))
	if err != nil {
		return nil, err
	}

	var content strings.Builder
	var reasoning strings.Builder
	var toolCalls []msg.ToolCall
	var usage *llm.TokenUsage

	for chunk := range ch {
		switch chunk.Type {
		case llm.ChunkText:
			content.WriteString(chunk.Delta)
			if a.bus != nil {
				_ = a.bus.Pub(bus.Msg{
					Type:    bus.TypeStreamChunk,
					From:    a.id,
					To:      src,
					Payload: chunk.Delta,
				})
			}
		case llm.ChunkToolCall:
			if chunk.ToolCall != nil {
				toolCalls = append(toolCalls, *chunk.ToolCall)
			}
			if chunk.ReasoningContent != "" {
				reasoning.WriteString(chunk.ReasoningContent)
			}
		case llm.ChunkDone:
			if chunk.Usage != nil {
				usage = chunk.Usage
			}
			if chunk.ReasoningContent != "" {
				reasoning.WriteString(chunk.ReasoningContent)
			}
			if a.bus != nil {
				_ = a.bus.Pub(bus.Msg{
					Type: bus.TypeStreamEnd,
					From: a.id,
					To:   src,
				})
			}
		case llm.ChunkError:
			return nil, chunk.Error
		}
	}

	return &llm.Response{
		Content:          content.String(),
		ReasoningContent: reasoning.String(),
		ToolCalls:        toolCalls,
		Usage:            usage,
	}, nil
}

func (a *Agent) buildPrompt() string {
	var b strings.Builder
	b.WriteString("You are a helpful assistant.\n\n")

	// Context info
	pwd, _ := os.Getwd()
	if pwd != "" {
		fmt.Fprintf(&b, "Working directory: %s\n", pwd)
	}
	fmt.Fprintf(&b, "Current time: %s\n\n", time.Now().Format("2006-01-02 15:04:05"))

	if a.prompt != "" {
		b.WriteString(a.prompt)
		b.WriteString("\n\n")
	}

	b.WriteString("Available tools:\n")
	for _, t := range a.reg.All() {
		fmt.Fprintf(&b, "- %s: %s\n", t.Name(), t.Desc())
	}

	if a.reg.Get("spawn") != nil {
		b.WriteString("\n## Multi-Agent Collaboration\n")
		b.WriteString("Use `spawn` to delegate subtasks to new agents. Good for:\n")
		b.WriteString("- Parallel work: spawn multiple agents to handle independent tasks\n")
		b.WriteString("- Complex tasks: break down into smaller subtasks\n")
		b.WriteString("- Focused work: let each agent focus on one specific thing\n")
		b.WriteString("Example: spawn({\"task\": \"analyze error handling in cmd/\", \"share_context\": false})\n")
	}

	if a.reg.Get("background") != nil {
		b.WriteString("\n## Background Tasks\n")
		b.WriteString("Use `background` for long-running commands. Returns immediately with task_id.\n")
		b.WriteString("When task completes, you'll receive a notification with the result.\n")
		b.WriteString("Good for: downloads, builds, tests, or any command that takes time.\n")
		b.WriteString("Example: background({\"cmd\": \"go build ./...\", \"cwd\": \"/path/to/project\"})\n")
	}

	if a.router != nil {
		b.WriteString("\n## Commands (PREFERRED over bash tool)\n")
		b.WriteString("Execute shell commands in code blocks with `!` prefix:\n")
		b.WriteString("```bash\n!ls -la\n!git status\n```\n")
		b.WriteString("IMPORTANT: Always use `!command` format instead of bash tool.\n")
		b.WriteString("The `!` prefix is REQUIRED. Without it, commands won't execute.\n")
		b.WriteString("Use `!compact [note]` to manually compact conversation context when history gets long.\n")
		b.WriteString("Use `!tokens` to inspect estimated token usage and decide when to compact.\n")
	}

	return b.String()
}

func (a *Agent) execTool(ctx context.Context, tc msg.ToolCall) (string, error) {
	t := a.reg.Get(tc.Name)
	if t == nil {
		return "", fmt.Errorf("unknown tool: %s", tc.Name)
	}

	timeout := time.Duration(a.cfg.Timeout.Tool) * time.Second
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	out, err := t.Run(ctx, tc.Args)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", tool.TimeoutError(tc.Name, string(tc.Args), a.cfg.Timeout.Tool)
		}
		return out, err
	}
	return out, nil
}

func (a *Agent) detectAndExecCommands(ctx context.Context, src, content string) string {
	cmds := parseCommands(content)
	if len(cmds) == 0 {
		return ""
	}

	ctx = command.WithSource(ctx, src)

	var results []string
	for _, raw := range cmds {
		out, ok, err := a.router.Route(ctx, raw)
		if !ok {
			continue
		}

		if a.bus != nil {
			_ = a.bus.Pub(bus.Msg{
				Type:    bus.TypeCommand,
				From:    a.id,
				To:      src,
				Payload: raw,
			})
		}

		status := "ok"
		if err != nil {
			status = "error"
			out = err.Error()
			if a.bus != nil {
				_ = a.bus.Pub(bus.Msg{
					Type:    bus.TypeCommandError,
					From:    a.id,
					To:      src,
					Payload: out,
				})
			}
		} else {
			if a.bus != nil {
				_ = a.bus.Pub(bus.Msg{
					Type:    bus.TypeCommandOK,
					From:    a.id,
					To:      src,
					Payload: out,
				})
			}
		}
		results = append(results, fmt.Sprintf("<command cmd=%q status=%q>\n%s\n</command>", raw, status, out))
	}

	if len(results) == 0 {
		return ""
	}

	feedback := strings.Join(results, "\n")
	a.session.Add(msg.Message{Role: "user", Content: feedback})
	return feedback
}

func parseCommands(content string) []string {
	var cmds []string
	lines := strings.Split(content, "\n")
	inFence := false

	for _, line := range lines {
		stripped := strings.TrimSpace(line)
		if fenceRe.MatchString(stripped) {
			inFence = !inFence
			continue
		}

		if inFence && command.IsCommand(stripped) {
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

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
