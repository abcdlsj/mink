package llm

import (
	"context"
	"io"

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

		st := openRouterStreamState{streamState: newStreamState()}
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
	streamState
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
		if !s.pushReasoning(ctx, ch, *delta.Reasoning) {
			return false
		}
	}
	if !s.pushReasoning(ctx, ch, delta.ReasoningContent) {
		return false
	}
	if !s.pushText(ctx, ch, delta.Content) {
		return false
	}
	for _, tc := range delta.ToolCalls {
		idx := 0
		if tc.Index != nil {
			idx = *tc.Index
		}
		s.addToolCall(idx, tc.ID, tc.Function.Name, tc.Function.Arguments)
	}
	return true
}
