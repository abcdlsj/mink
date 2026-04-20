package llm

import (
	"context"
	"io"
	"sort"
	"strings"

	"github.com/abcdlsj/mink/msg"
	"github.com/sashabaranov/go-openai"
)

func (o *openAI) ChatStream(ctx context.Context, msgs []msg.Message, tools []Tool) (<-chan Chunk, error) {
	req := o.buildRequest(msgs, tools)
	req.StreamOptions = &openai.StreamOptions{IncludeUsage: true}

	stream, err := o.client.CreateChatCompletionStream(ctx, req)
	if err != nil {
		req.StreamOptions = nil
		stream, err = o.client.CreateChatCompletionStream(ctx, req)
	}
	if err != nil {
		return nil, wrapOpenAIErr(err)
	}

	ch := make(chan Chunk, 32)
	go func() {
		defer stream.Close()
		defer close(ch)

		st := openAIStreamState{toolCalls: map[int]*msg.ToolCall{}}
		for {
			chunk, err := stream.Recv()
			if err != nil {
				if err != io.EOF {
					select {
					case ch <- Chunk{Type: ChunkError, Error: err}:
					case <-ctx.Done():
					}
				}
				break
			}
			if ok := st.updateUsage(chunk); ok {
				continue
			}
			st.pushDelta(ctx, ch, chunk)
		}
		st.flush(ctx, ch)
	}()

	return ch, nil
}

type openAIStreamState struct {
	reasoning strings.Builder
	usage     *TokenUsage
	toolCalls map[int]*msg.ToolCall
}

func (s *openAIStreamState) updateUsage(chunk openai.ChatCompletionStreamResponse) bool {
	if len(chunk.Choices) == 0 {
		if chunk.Usage != nil {
			s.usage = toTokenUsage(*chunk.Usage)
		}
		return true
	}
	if chunk.Usage != nil {
		s.usage = toTokenUsage(*chunk.Usage)
	}
	return false
}

func (s *openAIStreamState) pushDelta(ctx context.Context, ch chan<- Chunk, chunk openai.ChatCompletionStreamResponse) {
	delta := chunk.Choices[0].Delta
	if delta.ReasoningContent != "" {
		s.reasoning.WriteString(delta.ReasoningContent)
		if !emitChunk(ctx, ch, Chunk{Type: ChunkReasoning, ReasoningDelta: delta.ReasoningContent}) {
			return
		}
	}
	if delta.Content != "" {
		if !emitChunk(ctx, ch, Chunk{Type: ChunkText, Delta: delta.Content}) {
			return
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
}

func (s *openAIStreamState) flush(ctx context.Context, ch chan<- Chunk) {
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

func emitChunk(ctx context.Context, ch chan<- Chunk, chunk Chunk) bool {
	select {
	case ch <- chunk:
		return true
	case <-ctx.Done():
		return false
	}
}
