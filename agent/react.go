package agent

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/config"
	"github.com/abcdlsj/mink/hook"
	"github.com/abcdlsj/mink/llm"
	"github.com/abcdlsj/mink/msg"
	"github.com/abcdlsj/mink/tool"
)

func (a *Agent) step(ctx context.Context, src string) (bool, error) {
	msgs := a.session.Messages()
	sysMsgs := []msg.Message{{Role: "system", Content: a.buildPrompt(src)}}
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
			Role:               "assistant",
			Content:            r.Content,
			ReasoningContent:   r.ReasoningContent,
			ReasoningSignature: r.ReasoningSignature,
			ToolCalls:          r.ToolCalls,
		})
	}

	if r.Content != "" {
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

	results := make([]msg.ToolResult, 0, len(r.ToolCalls))

	for _, tc := range r.ToolCalls {
		a.hooks.Trigger(ctx, hook.BeforeTool, tc)
		if a.bus != nil {
			_ = a.bus.Pub(bus.Msg{
				Type: bus.TypeToolCall,
				From: a.id,
				To:   src,
				Payload: map[string]string{
					"id":   tc.ID,
					"name": tc.Name,
					"args": string(tc.Args),
				},
			})
		}

		out, toolErr := a.execTool(ctx, tc)

		tr := msg.ToolResult{ToolCallID: tc.ID, Content: out}
		if toolErr != nil {
			tr.Content = tool.FormatErrorForLLM(tc.Name, toolErr)
			tr.Error = toolErr.Error()
			if a.bus != nil {
				_ = a.bus.Pub(bus.Msg{
					Type: bus.TypeToolError,
					From: a.id,
					To:   src,
					Payload: map[string]string{
						"id":    tc.ID,
						"error": toolErr.Error(),
					},
				})
			}
		} else {
			if a.bus != nil {
				_ = a.bus.Pub(bus.Msg{
					Type: bus.TypeToolResult,
					From: a.id,
					To:   src,
					Payload: map[string]string{
						"id": tc.ID,
					},
				})
			}
		}
		a.hooks.Trigger(ctx, hook.AfterTool, tr)
		results = append(results, tr)
	}

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
	var signature string
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
			if chunk.ReasoningDelta != "" {
				reasoning.WriteString(chunk.ReasoningDelta)
				if a.bus != nil {
					_ = a.bus.Pub(bus.Msg{
						Type:    bus.TypeThinkingChunk,
						From:    a.id,
						To:      src,
						Payload: chunk.ReasoningDelta,
					})
				}
			}
		case llm.ChunkDone:
			if chunk.Usage != nil {
				usage = chunk.Usage
			}
			if chunk.ReasoningContent != "" {
				reasoning.WriteString(chunk.ReasoningContent)
			}
			if chunk.ReasoningSignature != "" {
				signature = chunk.ReasoningSignature
			}
			if a.bus != nil {
				_ = a.bus.Pub(bus.Msg{
					Type: bus.TypeStreamEnd,
					From: a.id,
					To:   src,
				})
				if reasoning.Len() > 0 {
					_ = a.bus.Pub(bus.Msg{
						Type:    bus.TypeThinkingEnd,
						From:    a.id,
						To:      src,
						Payload: reasoning.String(),
					})
				}
			}
		case llm.ChunkError:
			return nil, chunk.Error
		}
	}

	return &llm.Response{
		Content:            content.String(),
		ReasoningContent:   reasoning.String(),
		ReasoningSignature: signature,
		ToolCalls:          toolCalls,
		Usage:              usage,
	}, nil
}

func (a *Agent) buildPrompt(src string) string {
	var b strings.Builder
	b.WriteString("You are a helpful assistant. Be direct and concise. Avoid unnecessary small talk or redundant tool calls.\n\n")

	// Context info
	pwd, _ := os.Getwd()
	if pwd != "" {
		fmt.Fprintf(&b, "Working directory: %s\n", pwd)
	}
	fmt.Fprintf(&b, "Current time: %s\n\n", time.Now().Format("2006-01-02 15:04:05"))

	if soul := loadSoulPrompt(); soul != "" {
		b.WriteString("## Persona\n")
		b.WriteString("If ~/.mink/SOUL.md guidance does not conflict with higher-priority instructions, embody it naturally:\n")
		b.WriteString(soul)
		b.WriteString("\n\n")
	}

	if strings.EqualFold(a.cfg.Mode, "tg") && strings.HasPrefix(src, "telegram:") && !a.subAgent {
		b.WriteString("## Telegram Group Interaction\n")
		b.WriteString("Incoming user messages may include a [telegram_context]...[/telegram_context] metadata block. Use it to understand sender, mention status, message id, thread id, and reply chain.\n")
		b.WriteString("You are allowed to participate in group chats even without @mention, but stay selective.\n")
		b.WriteString("If no reply is needed, respond with exactly: NO_REPLY\n")
		b.WriteString("To control reply target, add [[reply_to_current]] or [[reply_to:<message_id>]].\n")
		b.WriteString("You can ask Telegram adapter to react by adding one directive tag: [[react:👀]] or [[react:👍]].\n")
		b.WriteString("If both reaction and text reply are needed, include the react tag and normal text together.\n")
		b.WriteString("These directive tags are internal and will be stripped before sending. Never explain them to users.\n\n")
	}

	if a.prompt != "" {
		b.WriteString(a.prompt)
		b.WriteString("\n\n")
	}

	b.WriteString("Available tools:\n")
	b.WriteString("Only use tools when truly necessary. Don't call tools just to \"check\" or \"explore\" if you already know the answer or can reason it out.\n")
	for _, t := range a.reg.All() {
		fmt.Fprintf(&b, "- %s: %s\n", t.Name(), t.Desc())
	}
	b.WriteString("\n")

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

	return b.String()
}

func loadSoulPrompt() string {
	data, err := os.ReadFile(config.SoulPath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func (a *Agent) execTool(ctx context.Context, tc msg.ToolCall) (string, error) {
	timeout := time.Duration(a.cfg.Timeout.Tool) * time.Second
	if tc.Name == "spawn" {
		timeout = 0
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	out, err := a.reg.Run(ctx, tc.Name, tc.Args)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", tool.TimeoutError(tc.Name, string(tc.Args), a.cfg.Timeout.Tool)
		}
		return out, err
	}
	return out, nil
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
