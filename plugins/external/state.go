package external

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/abcdlsj/mink/agent"
	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/msg"
	"github.com/abcdlsj/mink/session"
)

type toolCallState struct {
	call msg.ToolCall
	out  string
}

type runState struct {
	assistant strings.Builder
	reasoning strings.Builder
	order     []string
	calls     map[string]toolCallState
	streamed  bool
}

func (s *runState) onStream(turn *agent.Turn, text string) {
	s.assistant.WriteString(text)
	s.streamed = true
	agent.Publish(turn, bus.Event{Type: bus.TurnChunk, Text: text})
}

func (s *runState) onAssistant(turn *agent.Turn, text string, snapshot bool) {
	if snapshot {
		s.mergeAssistant(turn, text)
		return
	}
	s.assistant.WriteString(text)
	if !s.streamed {
		agent.Publish(turn, bus.Event{Type: bus.TurnChunk, Text: text})
	}
}

func (s *runState) onThinking(turn *agent.Turn, text string) {
	if text == "" {
		return
	}
	s.reasoning.WriteString(text)
	agent.Publish(turn, bus.Event{Type: bus.TurnReasoning, Text: text})
}

func (s *runState) mergeAssistant(turn *agent.Turn, text string) {
	if text == "" {
		return
	}
	cur := s.assistant.String()
	switch {
	case cur == "":
		s.assistant.WriteString(text)
		if !s.streamed {
			agent.Publish(turn, bus.Event{Type: bus.TurnChunk, Text: text})
		}
	case text == cur, strings.HasPrefix(cur, text):
		return
	case strings.HasPrefix(text, cur):
		extra := text[len(cur):]
		s.assistant.WriteString(extra)
		if extra != "" {
			agent.Publish(turn, bus.Event{Type: bus.TurnChunk, Text: extra})
		}
	case !s.streamed:
		s.assistant.WriteString(text)
		agent.Publish(turn, bus.Event{Type: bus.TurnChunk, Text: text})
	}
}

func (s *runState) onToolCall(turn *agent.Turn, m *Message) {
	if m.ToolID == "" {
		m.ToolID = uuid.New().String()[:8]
	}
	if _, ok := s.calls[m.ToolID]; !ok {
		s.order = append(s.order, m.ToolID)
	}
	s.calls[m.ToolID] = toolCallState{
		call: msg.ToolCall{
			ID:   m.ToolID,
			Name: m.ToolName,
			Args: json.RawMessage(m.ToolArgs),
		},
	}
	agent.Publish(turn, bus.Event{
		Type:       bus.ToolCallStarted,
		ToolCallID: m.ToolID,
		Tool:       m.ToolName,
		Input:      m.ToolArgs,
	})
}

func (s *runState) onToolResult(turn *agent.Turn, m *Message) {
	tc := s.calls[m.ToolID]
	tc.out = m.Text
	s.calls[m.ToolID] = tc
	agent.Publish(turn, bus.Event{
		Type:       bus.ToolCallFinished,
		ToolCallID: m.ToolID,
		Tool:       tc.call.Name,
		Input:      string(tc.call.Args),
		Output:     m.Text,
	})
}

func (s *runState) flush(sess *session.Session) {
	s.addAssistant(sess)
	s.addToolResults(sess)
}

func (s *runState) addAssistant(sess *session.Session) {
	if sess == nil || (strings.TrimSpace(s.assistant.String()) == "" && strings.TrimSpace(s.reasoning.String()) == "" && len(s.calls) == 0) {
		return
	}
	sess.Add(msg.Message{
		ID:        uuid.New().String()[:8],
		Role:      "assistant",
		Content:   s.assistant.String(),
		Reasoning: s.reasoning.String(),
		ToolCalls: s.toolCalls(),
		Timestamp: time.Now(),
	})
}

func (s *runState) addToolResults(sess *session.Session) {
	if sess == nil {
		return
	}
	results := s.toolResults()
	if len(results) == 0 {
		return
	}
	sess.Add(msg.Message{
		ID:          uuid.New().String()[:8],
		Role:        "tool",
		ToolResults: results,
		Timestamp:   time.Now(),
	})
}

func (s *runState) toolCalls() []msg.ToolCall {
	if len(s.calls) == 0 {
		return nil
	}
	out := make([]msg.ToolCall, 0, len(s.calls))
	for _, id := range s.stableIDs() {
		out = append(out, s.calls[id].call)
	}
	return out
}

func (s *runState) toolResults() []msg.ToolResult {
	if len(s.calls) == 0 {
		return nil
	}
	out := make([]msg.ToolResult, 0, len(s.calls))
	for _, id := range s.stableIDs() {
		call := s.calls[id]
		if call.out == "" {
			continue
		}
		out = append(out, msg.ToolResult{
			ToolCallID: id,
			Content:    call.out,
		})
	}
	return out
}

func (s *runState) stableIDs() []string {
	if len(s.order) != 0 {
		out := make([]string, 0, len(s.order))
		for _, id := range s.order {
			if _, ok := s.calls[id]; ok {
				out = append(out, id)
			}
		}
		return out
	}
	out := make([]string, 0, len(s.calls))
	for id := range s.calls {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func addUser(s *session.Session, input string) {
	if s == nil || strings.TrimSpace(input) == "" {
		return
	}
	s.Add(agent.NewUserMessage(input))
}

func wrapMessageError(name string, m *Message) error {
	err := m.Error
	if err == nil && m != nil && m.Text != "" {
		err = errors.New(m.Text)
	}
	if err == nil {
		err = fmt.Errorf("%s runtime failed", name)
	}
	return err
}
