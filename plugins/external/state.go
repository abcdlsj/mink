package external

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/abcdlsj/sumi/agent"
	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/session"
)

type toolCallState struct {
	call     msg.ToolCall
	out      string
	stderr   string
	exitCode int
	isError  bool
}

type runState struct {
	assistant   strings.Builder
	reasoning   strings.Builder
	order       []string
	calls       map[string]toolCallState
	streamed    bool
	usage       *msg.TokenUsage
	model       string
	cost        float64
	reason      string
	runtimeMeta map[string]string
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
	text = msg.NormalizeMarkdown(text)
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
	case text == cur, strings.HasPrefix(cur, text), strings.HasSuffix(cur, text), alreadyHasAssistantText(cur, text):
		return
	case strings.HasPrefix(text, cur):
		extra := text[len(cur):]
		s.assistant.WriteString(extra)
		if extra != "" {
			agent.Publish(turn, bus.Event{Type: bus.TurnChunk, Text: extra})
		}
	case !s.streamed:
		if overlap := assistantOverlap(cur, text); overlap > 0 {
			text = text[overlap:]
		}
		added := assistantBoundary(cur, text) + text
		s.assistant.WriteString(added)
		agent.Publish(turn, bus.Event{Type: bus.TurnChunk, Text: added})
	}
}

func alreadyHasAssistantText(cur, text string) bool {
	text = strings.TrimSpace(text)
	if len([]rune(text)) < 80 {
		return false
	}
	return strings.Contains(cur, text)
}

func assistantOverlap(cur, text string) int {
	max := len(cur)
	if len(text) < max {
		max = len(text)
	}
	for n := max; n >= 16; n-- {
		if strings.HasSuffix(cur, text[:n]) {
			return n
		}
	}
	return 0
}

func assistantBoundary(cur, text string) string {
	if cur == "" || text == "" || strings.HasSuffix(cur, "\n") || strings.HasPrefix(text, "\n") {
		return ""
	}
	if startsMarkdownBlock(text) {
		return "\n\n"
	}
	return ""
}

func startsMarkdownBlock(text string) bool {
	t := strings.TrimLeft(text, " \t")
	if strings.HasPrefix(t, "#") || strings.HasPrefix(t, "|") || strings.HasPrefix(t, "```") {
		return true
	}
	return strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* ") || strings.HasPrefix(t, "1. ")
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
	tc.stderr = m.Stderr
	tc.exitCode = m.ExitCode
	tc.isError = m.IsError
	s.calls[m.ToolID] = tc
	ev := bus.Event{
		Type:       bus.ToolCallFinished,
		ToolCallID: m.ToolID,
		Tool:       tc.call.Name,
		Input:      string(tc.call.Args),
		Output:     m.Text,
	}
	if m.IsError {
		ev.Type = bus.ToolCallFailed
		ev.Err = m.Stderr
	}
	agent.Publish(turn, ev)
}

func (s *runState) onTurnDone(turn *agent.Turn, m *Message) {
	if m.Text != "" {
		s.mergeAssistant(turn, m.Text)
	}
	if m.Usage != nil {
		s.usage = mergeUsage(s.usage, m.Usage)
	}
	if m.Model != "" {
		s.model = m.Model
	}
	if m.CostUSD > 0 {
		s.cost = m.CostUSD
	}
	if m.Reason != "" {
		s.reason = m.Reason
		if reasonNotice(m.Reason) {
			agent.Publish(turn, bus.Event{Type: bus.ServiceNotice, Text: "runtime ended: " + m.Reason})
		}
	}
}

func reasonNotice(reason string) bool {
	switch reason {
	case "", "completed", "success":
		return false
	}
	return true
}

func (s *runState) onRuntimeMeta(turn *agent.Turn, m *Message) {
	if turn == nil || len(m.Meta) == 0 {
		return
	}
	if s.runtimeMeta == nil {
		s.runtimeMeta = map[string]string{}
	}
	for k, v := range m.Meta {
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if k == "" || v == "" {
			continue
		}
		s.runtimeMeta[k] = v
	}
	if len(s.runtimeMeta) == 0 {
		return
	}
	data, err := json.Marshal(s.runtimeMeta)
	if err != nil {
		return
	}
	agent.Publish(turn, bus.Event{Type: bus.RuntimeInfo, Text: string(data)})
}

func mergeUsage(prev, next *msg.TokenUsage) *msg.TokenUsage {
	if prev == nil {
		out := *next
		return &out
	}
	out := *prev
	if next.Input > out.Input {
		out.Input = next.Input
	}
	if next.Output > out.Output {
		out.Output = next.Output
	}
	if next.Total > out.Total {
		out.Total = next.Total
	}
	if next.Source != "" {
		out.Source = next.Source
	}
	if next.ContextWindow > out.ContextWindow {
		out.ContextWindow = next.ContextWindow
	}
	if next.MaxTokens > out.MaxTokens {
		out.MaxTokens = next.MaxTokens
	}
	return &out
}

func (s *runState) flush(sess *session.Session) {
	s.addAssistant(sess)
	s.addToolResults(sess)
}

func (s *runState) addAssistant(sess *session.Session) {
	if sess == nil || (strings.TrimSpace(s.assistant.String()) == "" && strings.TrimSpace(s.reasoning.String()) == "" && len(s.calls) == 0) {
		return
	}
	usage := s.usage
	if s.cost > 0 || s.model != "" {
		if usage == nil {
			usage = &msg.TokenUsage{}
		}
		if s.cost > 0 {
			usage.CostUSD = s.cost
		}
		if s.model != "" {
			usage.Model = s.model
		}
	}
	sess.Add(msg.Message{
		ID:          uuid.New().String()[:8],
		Role:        "assistant",
		Content:     msg.NormalizeMarkdown(s.assistant.String()),
		Reasoning:   s.reasoning.String(),
		ToolCalls:   s.toolCalls(),
		Usage:       usage,
		RuntimeMeta: copyRuntimeMeta(s.runtimeMeta),
		Timestamp:   time.Now(),
	})
}

func copyRuntimeMeta(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if k == "" || v == "" {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
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
		r := msg.ToolResult{
			ToolCallID: id,
			Content:    call.out,
		}
		if call.isError {
			r.Error = call.stderr
			if r.Error == "" {
				r.Error = call.out
			}
		}
		out = append(out, r)
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
