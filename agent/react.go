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

	r, err := a.requestStep(ctx, src, stepNum, allMsgs, provider, msgs, sysMsgs)
	if err != nil {
		return false, err
	}

	a.nextModel = "default"
	assistantContent := r.Content
	if assistantContent == "" && len(r.ToolCalls) == 0 {
		assistantContent = "抱歉，模型暂时无法响应，请稍后再试。"
		a.appendEvent(ctx, "llm.empty_fallback", "warn", nil)
	}

	a.recordAssistantTurn(ctx, src, r, assistantContent)
	a.publishThinkingFallback(ctx, src, r.Reasoning)
	a.publishAssistantOutput(ctx, src, stepNum, assistantContent)

	if len(r.ToolCalls) == 0 {
		return true, nil
	}
	return a.runToolCalls(ctx, src, stepNum, r.ToolCalls)
}

func (a *Agent) requestStep(ctx context.Context, src string, stepNum int, allMsgs []msg.Message, provider llm.Provider, msgs, sysMsgs []msg.Message) (*llm.Response, error) {
	llmTimeout := time.Duration(a.cfg.Timeout.LLM) * time.Second
	llmCtx := ctx
	if llmTimeout > 0 {
		var cancel context.CancelFunc
		llmCtx, cancel = context.WithTimeout(ctx, llmTimeout)
		defer cancel()
	}

	r, err := a.runLLMRequest(llmCtx, src, stepNum, allMsgs, provider)
	if err != nil {
		return nil, err
	}
	a.updateTokenBaseline(msgs, sysMsgs, r.Usage)

	if r.Content == "" && len(r.ToolCalls) == 0 {
		a.appendEvent(ctx, "llm.empty_response", "warn", map[string]any{"retry": true})
		r, err = a.runLLMRequest(llmCtx, src, stepNum, allMsgs, provider)
		if err != nil {
			return nil, err
		}
		a.updateTokenBaseline(msgs, sysMsgs, r.Usage)
	}
	return r, nil
}

func (a *Agent) runLLMRequest(ctx context.Context, src string, stepNum int, allMsgs []msg.Message, provider llm.Provider) (*llm.Response, error) {
	start := time.Now()
	corrID := a.logLLMRequest(stepNum, len(allMsgs), a.stream)

	var (
		r   *llm.Response
		err error
	)
	if a.stream {
		r, err = a.stepStream(ctx, src, allMsgs, provider)
	} else {
		r, err = provider.Chat(ctx, allMsgs, tools(a.reg))
	}
	if err != nil {
		a.logLLMError(stepNum, corrID, err, time.Since(start))
		return nil, err
	}
	a.logLLMResponse(stepNum, corrID, r, time.Since(start))
	return r, nil
}

func (a *Agent) recordAssistantTurn(ctx context.Context, src string, r *llm.Response, assistantContent string) {
	if len(r.ToolCalls) == 0 && assistantContent == "" {
		return
	}
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

func (a *Agent) publishThinkingFallback(ctx context.Context, src, reasoning string) {
	if a.bus == nil || a.stream || strings.TrimSpace(reasoning) == "" {
		return
	}
	from := speakerAgentID(ctx, a.id)
	_ = a.bus.Pub(bus.Msg{Type: bus.TypeThinkingChunk, From: from, To: src, Payload: reasoning})
	_ = a.bus.Pub(bus.Msg{Type: bus.TypeThinkingEnd, From: from, To: src, Payload: reasoning})
}

func (a *Agent) publishAssistantOutput(ctx context.Context, src string, stepNum int, assistantContent string) {
	if assistantContent == "" {
		return
	}
	if a.bus != nil && a.stream {
		from := speakerAgentID(ctx, a.id)
		_ = a.bus.Pub(bus.Msg{Type: bus.TypeStreamChunk, From: from, To: src, Payload: assistantContent})
		_ = a.bus.Pub(bus.Msg{Type: bus.TypeStreamEnd, From: from, To: src})
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

func (a *Agent) runToolCalls(ctx context.Context, src string, stepNum int, calls []msg.ToolCall) (bool, error) {
	results := make([]msg.ToolResult, 0, len(calls))
	handoffScheduled := false

	for _, tc := range calls {
		toolCorrID := a.logToolCall(stepNum, tc)
		a.maybeSwitchModel(tc)
		a.hooks.Trigger(ctx, hook.BeforeTool, tc)
		a.publishToolCall(ctx, src, tc)

		toolStart := time.Now()
		out, toolErr := a.execTool(ctx, tc)
		a.rememberToolCall(tc, out, toolErr)
		a.logToolResult(stepNum, toolCorrID, tc, out, toolErr, time.Since(toolStart))

		tr := a.handleToolResult(ctx, src, tc, out, toolErr)
		a.hooks.Trigger(ctx, hook.AfterTool, tr)
		results = append(results, tr)
		if tc.Name == "mention" || tc.Name == "invite_agent" || tc.Name == "spawn_specialist" {
			handoffScheduled = true
		}
	}

	for _, tr := range results {
		a.session.Add(msg.Message{Role: "tool", AgentID: speakerAgentID(ctx, a.id), ToolResults: []msg.ToolResult{tr}})
	}
	if handoffScheduled {
		return true, nil
	}
	return false, nil
}

func (a *Agent) maybeSwitchModel(tc msg.ToolCall) {
	if tc.Args == nil {
		return
	}
	var p map[string]json.RawMessage
	if json.Unmarshal(tc.Args, &p); p == nil {
		return
	}
	if v, ok := p["_model"]; ok {
		a.nextModel = strings.Trim(string(v), `"`)
	}
}

func (a *Agent) publishToolCall(ctx context.Context, src string, tc msg.ToolCall) {
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
}

func (a *Agent) handleToolResult(ctx context.Context, src string, tc msg.ToolCall, out string, toolErr error) msg.ToolResult {
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
		return tr
	}

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
	return tr
}
