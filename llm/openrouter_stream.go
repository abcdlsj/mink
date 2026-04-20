package llm

import (
	"context"
	"io"
	"sort"
	"strings"

	"github.com/abcdlsj/mink/msg"
	openrouter "github.com/revrost/go-openrouter"
)

func (o *openRouter) ChatStream(ctx context.Context, msgs []msg.Message, tools []Tool) (<-chan Chunk, error) {
	req := o.buildRequest(msgs, tools)
	req.Stream = true
	req.StreamOptions = &openrouter.StreamOptions{IncludeUsage: true}

	stream, err := o.client.CreateChatCompletionStream(ctx, req)
	if err != nil {
		return nil, err
	}

	ch := make(chan Chunk, 32)
	go func() {
		defer stream.Close()
		defer close(ch)

		st := openRouterStreamState{toolCalls: map[int]*msg.ToolCall{}}
		for {
			chunk, err := stream.Recv()
			if err != nil {
				if err != io.EOF {
					emitChunk(ctx, ch, Chunk{Type: ChunkError, Error: err})
				}
				break
			}
			if ok := st.updateUsage(chunk); ok {
				continue
			}
			if !st.pushDelta(ctx, ch, chunk.Choices[0].Delta) {
				return
			}
		}
		st.flush(ctx, ch)
	}()

	return ch, nil
}

type openRouterStreamState struct {
	reasoning strings.Builder
	usage     *TokenUsage
	toolCalls map[int]*msg.ToolCall
}

func (s *openRouterStreamState) updateUsage(chunk openrouter.ChatCompletionStreamResponse) bool {
	if len(chunk.Choices) == 0 {
		if chunk.Usage != nil {
			s.usage = toTokenUsageFromOR(chunk.Usage)
		}
		return true
	}
	if chunk.Usage != nil {
		s.usage = toTokenUsageFromOR(chunk.Usage)
	}
	return false
}

func (s *openRouterStreamState) pushDelta(ctx context.Context, ch chan<- Chunk, delta openrouter.ChatCompletionStreamChoiceDelta) bool {
	if delta.Reasoning != nil && *delta.Reasoning != "" {
		s.reasoning.WriteString(*delta.Reasoning)
		if !emitChunk(ctx, ch, Chunk{Type: ChunkReasoning, ReasoningDelta: *delta.Reasoning}) {
			return false
		}
	}
	if delta.ReasoningContent != "" {
		s.reasoning.WriteString(delta.ReasoningContent)
		if !emitChunk(ctx, ch, Chunk{Type: ChunkReasoning, ReasoningDelta: delta.ReasoningContent}) {
			return false
		}
	}
	if delta.Content != "" {
		if !emitChunk(ctx, ch, Chunk{Type: ChunkText, Delta: delta.Content}) {
			return false
		}
	}
	for _, tc := range delta.ToolCalls {
		idx := 0
		if tc.Index != nil {
			idx = *tc.Index
		}
		if s.toolCalls[idx] == nil {
			s.toolCalls[idx] = &msg.ToolCall{}
		}
		if tc.ID != "" {
			s.toolCalls[idx].ID = tc.ID
		}
		if tc.Function.Name != "" {
			s.toolCalls[idx].Name = tc.Function.Name
		}
		s.toolCalls[idx].Args = append(s.toolCalls[idx].Args, []byte(tc.Function.Arguments)...)
	}
	return true
}

func (s *openRouterStreamState) flush(ctx context.Context, ch chan<- Chunk) {
	var idxs []int
	for idx := range s.toolCalls {
		idxs = append(idxs, idx)
	}
	sort.Ints(idxs)
	for _, idx := range idxs {
		tc := s.toolCalls[idx]
		if tc == nil || tc.Name == "" || len(tc.Args) == 0 {
			continue
		}
		if !emitChunk(ctx, ch, Chunk{Type: ChunkToolCall, ToolCall: tc}) {
			return
		}
	}
	emitChunk(ctx, ch, Chunk{Type: ChunkDone, Usage: s.usage, Reasoning: s.reasoning.String()})
}
