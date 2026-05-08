package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/llm"
	"github.com/abcdlsj/sumi/msg"
)

type engine struct {
	env *RuntimeEnv
}

func (e *engine) run(ctx context.Context, t *Turn) error {
	if t == nil || t.Session == nil {
		return fmt.Errorf("turn requires session")
	}
	t.Session.Add(NewUserMessage(t.Input))
	for step := 0; step < e.env.MaxSteps; step++ {
		resp, err := e.step(ctx, t)
		if err != nil {
			return err
		}
		if len(resp.ToolCalls) == 0 {
			return nil
		}
	}
	return fmt.Errorf("max steps reached")
}

func (e *engine) step(ctx context.Context, t *Turn) (*llm.Response, error) {
	resp, err := e.stream(ctx, t)
	if err != nil {
		return nil, err
	}
	t.Session.Add(newAssistantMessage(resp))
	if len(resp.ToolCalls) > 0 {
		toolExecutor{tools: e.env.Tools, sink: turnSink{turn: t}}.run(ctx, t, resp.ToolCalls)
	}
	return resp, nil
}

func (e *engine) stream(ctx context.Context, t *Turn) (*llm.Response, error) {
	ch, err := e.env.Provider.ChatStream(ctx, e.messages(t), e.env.Tools.Definitions())
	if err != nil {
		return nil, err
	}
	return collect(ctx, turnSink{turn: t}, ch)
}

func (e *engine) messages(t *Turn) []msg.Message {
	sys := msg.Message{
		Role:    "system",
		Content: BuildSystemPrompt(e.env, t),
	}
	return append([]msg.Message{sys}, t.Session.Messages...)
}

func collect(ctx context.Context, sink turnSink, ch <-chan llm.Chunk) (*llm.Response, error) {
	var text strings.Builder
	var reasoning strings.Builder
	var calls []msg.ToolCall
	var usage *llm.TokenUsage

	for part := range ch {
		switch part.Type {
		case llm.ChunkText:
			text.WriteString(part.Delta)
			sink.Publish(bus.Event{Type: bus.TurnChunk, Text: part.Delta})
		case llm.ChunkReasoning:
			reasoning.WriteString(part.ReasoningDelta)
			sink.Publish(bus.Event{Type: bus.TurnReasoning, Text: part.ReasoningDelta})
		case llm.ChunkToolCall:
			if part.ToolCall != nil {
				calls = append(calls, *part.ToolCall)
			}
		case llm.ChunkDone:
			usage = part.Usage
		case llm.ChunkError:
			if part.Error != nil {
				return nil, part.Error
			}
		}
	}

	return &llm.Response{
		Content:   text.String(),
		Reasoning: reasoning.String(),
		ToolCalls: calls,
		Usage:     usage,
	}, ctx.Err()
}
