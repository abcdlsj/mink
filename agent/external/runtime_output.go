package external

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"

	agrt "github.com/abcdlsj/mink/agent/runtime"
	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/msg"
)

func (r *ExternalRuntime) readOutput(ctx context.Context, stdout io.Reader) (string, bool) {
	var sb strings.Builder
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 256*1024), 10*1024*1024)
	sawStream := false

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return sb.String(), sawStream
		default:
		}

		line := scanner.Text()

		if r.driver.ParseOutput != nil {
			ev := r.driver.ParseOutput(line)
			if ev == nil {
				continue
			}
			r.handleRuntimeMessage(ev)
			switch ev.Type {
			case agrt.MsgStreamChunk:
				sawStream = true
				sb.WriteString(ev.Text)
			case agrt.MsgAssistantText:
				if sawStream {
					continue
				}
				if sb.Len() > 0 {
					sb.WriteString("\n")
				}
				sb.WriteString(ev.Text)
			case agrt.MsgTurnDone:
				if sb.Len() == 0 && ev.Text != "" {
					sb.WriteString(ev.Text)
				}
			case agrt.MsgError:
				if sb.Len() > 0 {
					sb.WriteString("\n")
				}
				sb.WriteString("error: ")
				sb.WriteString(ev.Text)
			}
			continue
		}

		sb.WriteString(line)
		sb.WriteString("\n")

		if r.b != nil {
			_ = r.b.Pub(bus.Msg{
				Type:    bus.TypeStreamChunk,
				From:    r.agentID,
				To:      r.source,
				Payload: line + "\n",
			})
		}
	}

	if r.b != nil {
		_ = r.b.Pub(bus.Msg{
			Type: bus.TypeStreamEnd,
			From: r.agentID,
			To:   r.source,
		})
	}

	return strings.TrimSpace(sb.String()), sawStream
}

func (r *ExternalRuntime) handleRuntimeMessage(m *agrt.Message) {
	if m.SessionID != "" || m.InputTokens > 0 || m.OutputTokens > 0 {
		r.mu.Lock()
		if m.SessionID != "" {
			r.externalSessionID = m.SessionID
		}
		r.inputTokens += m.InputTokens
		r.outputTokens += m.OutputTokens
		r.mu.Unlock()
	}

	// Record tool events in session regardless of bus availability.
	if r.sess != nil {
		switch m.Type {
		case agrt.MsgToolCall:
			r.sess.Add(msg.Message{
				Role:      "assistant",
				ToolCalls: []msg.ToolCall{{ID: m.ToolID, Name: m.ToolName, Args: json.RawMessage(m.ToolArgs)}},
			})
		case agrt.MsgToolResult:
			r.sess.Add(msg.Message{
				Role:        "tool",
				ToolResults: []msg.ToolResult{{ToolCallID: m.ToolID, Content: m.Text}},
			})
		}
	}

	if r.b == nil {
		return
	}
	switch m.Type {
	case agrt.MsgStreamChunk:
		_ = r.b.Pub(bus.Msg{
			Type:    bus.TypeStreamChunk,
			From:    r.agentID,
			To:      r.source,
			Payload: m.Text,
		})
	case agrt.MsgThinkingChunk:
		_ = r.b.Pub(bus.Msg{
			Type:    bus.TypeThinkingChunk,
			From:    r.agentID,
			To:      r.source,
			Payload: m.Text,
		})
	case agrt.MsgToolCall:
		_ = r.b.Pub(bus.Msg{
			Type: bus.TypeToolCall,
			From: r.agentID,
			To:   r.source,
			Payload: map[string]string{
				"id":   m.ToolID,
				"name": m.ToolName,
				"args": m.ToolArgs,
			},
		})
	case agrt.MsgToolResult:
		_ = r.b.Pub(bus.Msg{
			Type: bus.TypeToolResult,
			From: r.agentID,
			To:   r.source,
			Payload: map[string]string{
				"id":     m.ToolID,
				"result": m.Text,
			},
		})
	case agrt.MsgError:
		_ = r.b.Pub(bus.Msg{
			Type:    bus.TypeAssistant,
			From:    r.agentID,
			To:      r.source,
			Payload: "error: " + m.Text,
		})
	}
}
