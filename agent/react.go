package agent

import (
	"context"
	"strings"
	"time"

	"github.com/abcdlsj/mink/bus"
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

	start := time.Now()
	if a.stream {
		r, err = a.stepStream(llmCtx, src, allMsgs)
	} else {
		r, err = a.p.Chat(llmCtx, allMsgs, tools(a.reg))
	}
	if err != nil {
		return false, err
	}
	a.logReq(sysMsgs[0].Content, len(allMsgs), a.stream, start, r)
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
