package llm

import (
	"context"
	"sort"
	"strings"

	"github.com/abcdlsj/mink/msg"
)

type streamState struct {
	reasoning strings.Builder
	usage     *TokenUsage
	toolCalls map[int]*msg.ToolCall
}

func newStreamState() streamState {
	return streamState{toolCalls: map[int]*msg.ToolCall{}}
}

func (s *streamState) pushReasoning(ctx context.Context, ch chan<- Chunk, text string) bool {
	if text == "" {
		return true
	}
	s.reasoning.WriteString(text)
	return emitChunk(ctx, ch, Chunk{Type: ChunkReasoning, ReasoningDelta: text})
}

func (s *streamState) pushText(ctx context.Context, ch chan<- Chunk, text string) bool {
	if text == "" {
		return true
	}
	return emitChunk(ctx, ch, Chunk{Type: ChunkText, Delta: text})
}

func (s *streamState) addToolCall(idx int, id, name, args string) {
	if s.toolCalls[idx] == nil {
		s.toolCalls[idx] = &msg.ToolCall{}
	}
	if id != "" {
		s.toolCalls[idx].ID = id
	}
	if name != "" {
		s.toolCalls[idx].Name = name
	}
	s.toolCalls[idx].Args = append(s.toolCalls[idx].Args, []byte(args)...)
}

func (s *streamState) flush(ctx context.Context, ch chan<- Chunk) {
	idxs := make([]int, 0, len(s.toolCalls))
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
