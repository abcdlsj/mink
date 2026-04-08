package agent

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/hook"
	"github.com/abcdlsj/mink/llm"
	"github.com/abcdlsj/mink/msg"
	"github.com/abcdlsj/mink/tool"
)

func (a *Agent) step(ctx context.Context, src string, stepNum int) (bool, error) {
	msgs := a.viewMessages()
	sysMsgs := []msg.Message{{Role: "system", Content: a.buildPrompt(ctx, src)}}
	allMsgs := append(sysMsgs, msgs...)

	provider := a.p
	if a.sel != nil {
		provider = a.sel.P(a.nextModel)
	}

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
	corrID := a.logLLMRequest(stepNum, len(allMsgs), a.stream)
	if a.stream {
		r, err = a.stepStream(llmCtx, src, allMsgs, provider)
	} else {
		r, err = provider.Chat(llmCtx, allMsgs, tools(a.reg))
	}
	if err != nil {
		a.logLLMError(stepNum, corrID, err, time.Since(start))
		return false, err
	}
	a.logLLMResponse(stepNum, corrID, r, time.Since(start))
	a.updateTokenBaseline(msgs, sysMsgs, r.Usage)

	// Retry once on empty response (no content, no tool calls).
	if r.Content == "" && len(r.ToolCalls) == 0 {
		a.appendEvent(ctx, "llm.empty_response", "warn", map[string]any{"retry": true})
		start2 := time.Now()
		corrID2 := a.logLLMRequest(stepNum, len(allMsgs), a.stream)
		if a.stream {
			r, err = a.stepStream(llmCtx, src, allMsgs, provider)
		} else {
			r, err = provider.Chat(llmCtx, allMsgs, tools(a.reg))
		}
		if err != nil {
			a.logLLMError(stepNum, corrID2, err, time.Since(start2))
			return false, err
		}
		a.logLLMResponse(stepNum, corrID2, r, time.Since(start2))
		a.updateTokenBaseline(msgs, sysMsgs, r.Usage)
	}

	a.nextModel = "default"
	assistantContent := r.Content

	// Fallback: if still empty after retry, send a visible message instead of silent drop.
	if assistantContent == "" && len(r.ToolCalls) == 0 {
		assistantContent = "抱歉，模型暂时无法响应，请稍后再试。"
		a.appendEvent(ctx, "llm.empty_fallback", "warn", nil)
	}

	if len(r.ToolCalls) > 0 || assistantContent != "" {
		a.session.Add(msg.Message{
			Role:               "assistant",
			AgentID:            speakerAgentID(ctx, a.id),
			Content:            assistantContent,
			Reasoning:          r.Reasoning,
			ReasoningSignature: r.ReasoningSignature,
			ToolCalls:          r.ToolCalls,
		})
		a.appendEvent(ctx, "assistant.emitted", "assistant", map[string]any{
			"content":       assistantContent,
			"tool_calls":    len(r.ToolCalls),
			"has_reasoning": r.Reasoning != "",
			"agent_id":      a.id,
		})
	}

	if assistantContent != "" {
		if a.bus != nil && a.stream {
			_ = a.bus.Pub(bus.Msg{
				Type:    bus.TypeStreamChunk,
				From:    speakerAgentID(ctx, a.id),
				To:      src,
				Payload: assistantContent,
			})
			_ = a.bus.Pub(bus.Msg{
				Type: bus.TypeStreamEnd,
				From: speakerAgentID(ctx, a.id),
				To:   src,
			})
		}

		a.logAgentOutput(stepNum, assistantContent)
		a.hooks.Trigger(ctx, hook.BeforeAssist, assistantContent)
		if a.bus != nil && !a.stream {
			_ = a.bus.Pub(bus.Msg{
				Type:    bus.TypeAssistant,
				From:    speakerAgentID(ctx, a.id),
				To:      src,
				Payload: assistantContent,
			})
		}
		a.hooks.Trigger(ctx, hook.AfterAssist, assistantContent)
	}

	if len(r.ToolCalls) == 0 {
		return true, nil
	}

	results := make([]msg.ToolResult, 0, len(r.ToolCalls))

	for _, tc := range r.ToolCalls {
		toolCorrID := a.logToolCall(stepNum, tc)

		if tc.Args != nil {
			var p map[string]json.RawMessage
			if json.Unmarshal(tc.Args, &p); p != nil {
				if v, ok := p["_model"]; ok {
					a.nextModel = strings.Trim(string(v), `"`)
				}
			}
		}

		a.hooks.Trigger(ctx, hook.BeforeTool, tc)
		if a.bus != nil {
			_ = a.bus.Pub(bus.Msg{
				Type: bus.TypeToolCall,
				From: speakerAgentID(ctx, a.id),
				To:   src,
				Payload: map[string]string{
					"id":   tc.ID,
					"name": tc.Name,
					"args": string(tc.Args),
				},
			})
		}
		a.appendEvent(ctx, "tool.called", "tool", map[string]any{
			"id":   tc.ID,
			"name": tc.Name,
			"args": string(tc.Args),
		})

		toolStart := time.Now()
		out, toolErr := a.execTool(ctx, tc)
		a.rememberToolCall(tc, out, toolErr)
		a.logToolResult(stepNum, toolCorrID, tc, out, toolErr, time.Since(toolStart))

		tr := msg.ToolResult{ToolCallID: tc.ID, Content: out}
		if toolErr != nil {
			tr.Content = tool.FormatErrorForLLM(tc.Name, toolErr)
			tr.Error = toolErr.Error()
			a.appendEvent(ctx, "tool.failed", "tool", map[string]any{
				"id":     tc.ID,
				"name":   tc.Name,
				"error":  toolErr.Error(),
				"output": out,
			})
			if a.bus != nil {
				_ = a.bus.Pub(bus.Msg{
					Type: bus.TypeToolError,
					From: speakerAgentID(ctx, a.id),
					To:   src,
					Payload: map[string]string{
						"id":    tc.ID,
						"error": toolErr.Error(),
					},
				})
			}
		} else {
			a.appendEvent(ctx, "tool.completed", "tool", map[string]any{
				"id":     tc.ID,
				"name":   tc.Name,
				"output": out,
			})
			if a.bus != nil {
				_ = a.bus.Pub(bus.Msg{
					Type: bus.TypeToolResult,
					From: speakerAgentID(ctx, a.id),
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
		a.session.Add(msg.Message{Role: "tool", AgentID: speakerAgentID(ctx, a.id), ToolResults: []msg.ToolResult{tr}})
	}

	return false, nil
}

func (a *Agent) stepStream(ctx context.Context, src string, allMsgs []msg.Message, provider llm.Provider) (*llm.Response, error) {
	ch, err := provider.ChatStream(ctx, allMsgs, tools(a.reg))
	if err != nil {
		return nil, err
	}

	var content strings.Builder
	var reasoning strings.Builder
	var signature string
	var toolCalls []msg.ToolCall
	var usage *llm.TokenUsage
	var reasoningDelta strings.Builder
	lastReasoningFlush := time.Now()

	flushReasoning := func(force bool) {
		if a.bus == nil || reasoningDelta.Len() == 0 {
			return
		}
		if !force {
			if time.Since(lastReasoningFlush) < 120*time.Millisecond && reasoningDelta.Len() < 128 {
				return
			}
		}
		_ = a.bus.Pub(bus.Msg{
			Type:    bus.TypeThinkingChunk,
			From:    a.id,
			To:      src,
			Payload: reasoningDelta.String(),
		})
		reasoningDelta.Reset()
		lastReasoningFlush = time.Now()
	}

	for chunk := range ch {
		switch chunk.Type {
		case llm.ChunkText:
			content.WriteString(chunk.Delta)
		case llm.ChunkToolCall:
			if chunk.ToolCall != nil {
				toolCalls = append(toolCalls, *chunk.ToolCall)
			}
		case llm.ChunkReasoning:
			reasoning.WriteString(chunk.ReasoningDelta)
			reasoningDelta.WriteString(chunk.ReasoningDelta)
			flushReasoning(false)
		case llm.ChunkDone:
			if chunk.Usage != nil {
				usage = chunk.Usage
			}
			if chunk.Reasoning != "" {
				full := chunk.Reasoning
				current := reasoning.String()
				switch {
				case current == "":
					reasoning.WriteString(full)
					reasoningDelta.WriteString(full)
				case strings.HasPrefix(full, current):
					extra := strings.TrimPrefix(full, current)
					reasoning.WriteString(extra)
					reasoningDelta.WriteString(extra)
				}
			}
			if chunk.ReasoningSignature != "" {
				signature = chunk.ReasoningSignature
			}
			flushReasoning(true)
			if a.bus != nil {
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
		Reasoning:          reasoning.String(),
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

	if err := a.maybeBlockDuplicateToolCall(tc); err != nil {
		return "", err
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
