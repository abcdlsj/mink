package llm

import (
	"context"
	"io"

	"github.com/abcdlsj/sumi/msg"
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

		st := openAIStreamState{streamState: newStreamState()}
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
	streamState
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
	if !s.pushReasoning(ctx, ch, delta.ReasoningContent) {
		return
	}
	if !s.pushText(ctx, ch, delta.Content) {
		return
	}
	for _, tc := range delta.ToolCalls {
		idx := 0
		if tc.Index != nil {
			idx = *tc.Index
		}
		s.addToolCall(idx, tc.ID, tc.Function.Name, tc.Function.Arguments)
	}
}
