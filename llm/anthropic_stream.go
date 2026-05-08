package llm

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/abcdlsj/sumi/msg"
	"github.com/anthropics/anthropic-sdk-go"
)

func (p *anthropicProvider) ChatStream(ctx context.Context, msgs []msg.Message, tools []Tool) (<-chan Chunk, error) {
	stream := p.client.Messages.NewStreaming(ctx, p.buildRequest(msgs, tools))
	ch := make(chan Chunk, 32)
	go func() {
		defer close(ch)

		var st anthropicStreamState
		for stream.Next() {
			if !st.pushEvent(ctx, ch, stream.Current()) {
				return
			}
		}
		if err := stream.Err(); err != nil {
			emitChunk(ctx, ch, Chunk{Type: ChunkError, Error: err})
			return
		}
		st.flush(ctx, ch)
	}()
	return ch, nil
}

type anthropicPendingTool struct {
	id   string
	name string
	args strings.Builder
}

type anthropicStreamState struct {
	pending   []anthropicPendingTool
	usage     *TokenUsage
	reasoning strings.Builder
	signature string
}

func (s *anthropicStreamState) pushEvent(ctx context.Context, ch chan<- Chunk, event anthropic.MessageStreamEventUnion) bool {
	switch ev := event.AsAny().(type) {
	case anthropic.ContentBlockStartEvent:
		return s.pushStart(ev)
	case anthropic.ContentBlockDeltaEvent:
		return s.pushDelta(ctx, ch, ev)
	case anthropic.MessageDeltaEvent:
		s.usage = &TokenUsage{
			InputTokens:  int(ev.Usage.InputTokens),
			OutputTokens: int(ev.Usage.OutputTokens),
			TotalTokens:  int(ev.Usage.InputTokens + ev.Usage.OutputTokens),
			InputSource:  "anthropic.stream",
		}
	}
	return true
}

func (s *anthropicStreamState) pushStart(ev anthropic.ContentBlockStartEvent) bool {
	block, ok := ev.ContentBlock.AsAny().(anthropic.ToolUseBlock)
	if !ok {
		return true
	}
	s.pending = append(s.pending, anthropicPendingTool{id: block.ID, name: block.Name})
	return true
}

func (s *anthropicStreamState) pushDelta(ctx context.Context, ch chan<- Chunk, ev anthropic.ContentBlockDeltaEvent) bool {
	switch delta := ev.Delta.AsAny().(type) {
	case anthropic.TextDelta:
		return emitChunk(ctx, ch, Chunk{Type: ChunkText, Delta: delta.Text})
	case anthropic.ThinkingDelta:
		s.reasoning.WriteString(delta.Thinking)
		return emitChunk(ctx, ch, Chunk{Type: ChunkReasoning, ReasoningDelta: delta.Thinking})
	case anthropic.InputJSONDelta:
		if len(s.pending) > 0 {
			s.pending[len(s.pending)-1].args.WriteString(delta.PartialJSON)
		}
	case anthropic.SignatureDelta:
		s.signature = delta.Signature
	}
	return true
}

func (s *anthropicStreamState) flush(ctx context.Context, ch chan<- Chunk) {
	for _, pt := range s.pending {
		if pt.name == "" || pt.args.Len() == 0 {
			continue
		}
		tc := msg.ToolCall{
			ID:   pt.id,
			Name: pt.name,
			Args: json.RawMessage(pt.args.String()),
		}
		if !emitChunk(ctx, ch, Chunk{Type: ChunkToolCall, ToolCall: &tc}) {
			return
		}
	}
	emitChunk(ctx, ch, Chunk{
		Type:               ChunkDone,
		Usage:              s.usage,
		Reasoning:          s.reasoning.String(),
		ReasoningSignature: s.signature,
	})
}
