package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/llm"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/session"
)

type engine struct {
	env *RuntimeEnv
}

func (e *engine) run(ctx context.Context, t *Turn) error {
	if t == nil || t.Session == nil {
		return fmt.Errorf("turn requires session")
	}
	t.Session.Add(NewUserMessageWithAttachments(t.Input, t.Attachments))
	retriedWithoutImages := false
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		resp, err := e.step(ctx, t)
		if err != nil {
			if !retriedWithoutImages && imageInputUnsupported(err) && downgradeImageAttachments(t.Session) {
				retriedWithoutImages = true
				continue
			}
			return err
		}
		if len(resp.ToolCalls) == 0 {
			return nil
		}
	}
}

const imageUnsupportedNote = "[System note: Image payloads were omitted because the current model endpoint does not support image input. Do not infer visual details; tell the user you cannot inspect the image and ask for a text description or a vision-capable model.]"

func imageInputUnsupported(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "image_url") && (strings.Contains(s, "expected text") || strings.Contains(s, "unknown variant"))
}

func downgradeImageAttachments(s *session.Session) bool {
	if s == nil {
		return false
	}
	changed := false
	for i := range s.Messages {
		m := &s.Messages[i]
		if m.Role != "user" || len(m.Attachments) == 0 {
			continue
		}
		kept := m.Attachments[:0]
		dropped := false
		for _, a := range m.Attachments {
			if a.Kind == "image" && (a.URL != "" || (a.Data != "" && a.MIME != "")) {
				dropped = true
				continue
			}
			kept = append(kept, a)
		}
		if !dropped {
			continue
		}
		m.Attachments = kept
		if len(m.Attachments) == 0 {
			m.Attachments = nil
		}
		if !strings.Contains(m.Content, imageUnsupportedNote) {
			m.Content = strings.TrimSpace(m.Content) + "\n\n" + imageUnsupportedNote
		}
		changed = true
	}
	return changed
}

func (e *engine) step(ctx context.Context, t *Turn) (*llm.Response, error) {
	resp, err := e.stream(ctx, t)
	if err != nil {
		return nil, err
	}
	if resp.Content != "" || resp.Reasoning != "" || len(resp.ToolCalls) > 0 {
		t.Session.Add(newAssistantMessage(resp))
	}
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
