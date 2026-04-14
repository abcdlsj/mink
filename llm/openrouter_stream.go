package llm

import (
	"context"
	"io"
	"sort"
	"strings"

	"github.com/abcdlsj/mink/msg"
)

func (o *openRouter) ChatStream(ctx context.Context, msgs []msg.Message, tools []Tool) (<-chan Chunk, error) {
	req := o.buildRequest(msgs, tools)
	req.Stream = true
	req.StreamOptions = streamOptions()

	stream, err := o.client.CreateChatCompletionStream(ctx, req)
	if err != nil {
		return nil, err
	}

	ch := make(chan Chunk, 32)
	go func() {
		defer stream.Close()
		defer close(ch)

		var reasoning strings.Builder
		var usage *TokenUsage
		toolCalls := make(map[int]*msg.ToolCall)

		for {
			chunk, err := stream.Recv()
			if err != nil {
				if err == io.EOF {
					break
				}
				select {
				case ch <- Chunk{Type: ChunkError, Error: err}:
				case <-ctx.Done():
				}
				break
			}

			if len(chunk.Choices) == 0 {
				if chunk.Usage != nil {
					usage = toTokenUsageFromOR(chunk.Usage)
				}
				continue
			}
			if chunk.Usage != nil {
				usage = toTokenUsageFromOR(chunk.Usage)
			}

			delta := chunk.Choices[0].Delta
			if delta.Reasoning != nil && *delta.Reasoning != "" {
				reasoning.WriteString(*delta.Reasoning)
				select {
				case ch <- Chunk{Type: ChunkReasoning, ReasoningDelta: *delta.Reasoning}:
				case <-ctx.Done():
					return
				}
			}
			if delta.ReasoningContent != "" {
				reasoning.WriteString(delta.ReasoningContent)
				select {
				case ch <- Chunk{Type: ChunkReasoning, ReasoningDelta: delta.ReasoningContent}:
				case <-ctx.Done():
					return
				}
			}
			if delta.Content != "" {
				select {
				case ch <- Chunk{Type: ChunkText, Delta: delta.Content}:
				case <-ctx.Done():
					return
				}
			}
			for _, tc := range delta.ToolCalls {
				idx := 0
				if tc.Index != nil {
					idx = *tc.Index
				}
				if _, ok := toolCalls[idx]; !ok {
					toolCalls[idx] = &msg.ToolCall{}
				}
				if tc.ID != "" {
					toolCalls[idx].ID = tc.ID
				}
				if tc.Function.Name != "" {
					toolCalls[idx].Name = tc.Function.Name
				}
				toolCalls[idx].Args = append(toolCalls[idx].Args, []byte(tc.Function.Arguments)...)
			}
		}

		idxs := make([]int, 0, len(toolCalls))
		for idx := range toolCalls {
			idxs = append(idxs, idx)
		}
		sort.Ints(idxs)
		for _, idx := range idxs {
			tc := toolCalls[idx]
			if tc == nil || tc.Name == "" || len(tc.Args) == 0 {
				continue
			}
			select {
			case ch <- Chunk{Type: ChunkToolCall, ToolCall: tc}:
			case <-ctx.Done():
				return
			}
		}

		select {
		case ch <- Chunk{Type: ChunkDone, Usage: usage, Reasoning: reasoning.String()}:
		case <-ctx.Done():
		}
	}()

	return ch, nil
}
