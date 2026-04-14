package agent

import (
	"context"
	"strings"
	"time"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/llm"
	"github.com/abcdlsj/mink/msg"
)

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
		if !force && time.Since(lastReasoningFlush) < 120*time.Millisecond && reasoningDelta.Len() < 128 {
			return
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
			if a.bus != nil && reasoning.Len() > 0 {
				_ = a.bus.Pub(bus.Msg{
					Type:    bus.TypeThinkingEnd,
					From:    a.id,
					To:      src,
					Payload: reasoning.String(),
				})
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
