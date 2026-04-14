package agent

import (
	"bufio"
	"context"
	"io"
	"strings"

	"github.com/abcdlsj/mink/bus"
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
			case MsgStreamChunk:
				sawStream = true
				sb.WriteString(ev.Text)
			case MsgAssistantText:
				if sawStream {
					continue
				}
				if sb.Len() > 0 {
					sb.WriteString("\n")
				}
				sb.WriteString(ev.Text)
			case MsgTurnDone:
				if sb.Len() == 0 && ev.Text != "" {
					sb.WriteString(ev.Text)
				}
			case MsgError:
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

func (r *ExternalRuntime) handleRuntimeMessage(m *RuntimeMessage) {
	if m.SessionID != "" || m.InputTokens > 0 || m.OutputTokens > 0 {
		r.mu.Lock()
		if m.SessionID != "" {
			r.externalSessionID = m.SessionID
		}
		r.inputTokens += m.InputTokens
		r.outputTokens += m.OutputTokens
		r.mu.Unlock()
	}
	if r.b == nil {
		return
	}
	switch m.Type {
	case MsgStreamChunk:
		_ = r.b.Pub(bus.Msg{
			Type:    bus.TypeStreamChunk,
			From:    r.agentID,
			To:      r.source,
			Payload: m.Text,
		})
	case MsgThinkingChunk:
		_ = r.b.Pub(bus.Msg{
			Type:    bus.TypeThinkingChunk,
			From:    r.agentID,
			To:      r.source,
			Payload: m.Text,
		})
	case MsgToolCall:
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
	case MsgToolResult:
		_ = r.b.Pub(bus.Msg{
			Type: bus.TypeToolResult,
			From: r.agentID,
			To:   r.source,
			Payload: map[string]string{
				"id":     m.ToolID,
				"result": m.Text,
			},
		})
	case MsgError:
		_ = r.b.Pub(bus.Msg{
			Type:    bus.TypeAssistant,
			From:    r.agentID,
			To:      r.source,
			Payload: "error: " + m.Text,
		})
	}
}
