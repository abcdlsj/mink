package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/msg"
	"github.com/abcdlsj/mink/platform"
	rtsqlite "github.com/abcdlsj/mink/runtime/store/sqlite"
)

func (a *App) webMessagesForSource(src string) []platform.WebMessage {
	sess, err := a.sm.Current(src)
	if err != nil || sess == nil {
		return nil
	}
	msgs := sess.View().Messages
	out := make([]platform.WebMessage, 0, len(msgs))
	for _, message := range msgs {
		webMsg := platform.WebMessage{
			Role:        message.Role,
			Sender:      a.webSenderName(message),
			Descriptor:  a.webDescriptor(message),
			Time:        webTime(message.Timestamp),
			Content:     strings.TrimSpace(message.Content),
			Reasoning:   strings.TrimSpace(message.Reasoning),
			ToolCalls:   a.webToolCalls(message.ToolCalls),
			ToolResults: a.webToolResults(message.ToolResults),
		}
		if webMsg.Content == "" {
			webMsg.Content = a.webFallbackContent(message)
		}
		if !a.webMessageVisible(webMsg) {
			continue
		}
		out = append(out, webMsg)
	}
	return out
}

func (a *App) webMessageVisible(message platform.WebMessage) bool {
	return message.Content != "" ||
		message.Reasoning != "" ||
		len(message.ToolCalls) > 0 ||
		len(message.ToolResults) > 0
}

func (a *App) webToolCalls(calls []msg.ToolCall) []platform.WebToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]platform.WebToolCall, 0, len(calls))
	for _, call := range calls {
		args := strings.TrimSpace(string(call.Args))
		if args == "" || args == "null" {
			args = ""
		}
		out = append(out, platform.WebToolCall{
			Name: call.Name,
			Args: args,
		})
	}
	return out
}

func (a *App) webToolResults(results []msg.ToolResult) []platform.WebToolResult {
	if len(results) == 0 {
		return nil
	}
	out := make([]platform.WebToolResult, 0, len(results))
	for _, result := range results {
		out = append(out, platform.WebToolResult{
			Content: strings.TrimSpace(result.Content),
			Error:   strings.TrimSpace(result.Error),
		})
	}
	return out
}

func (a *App) webFallbackContent(message msg.Message) string {
	switch message.Role {
	case "assistant":
		if len(message.ToolCalls) == 0 {
			return ""
		}
		lines := make([]string, 0, len(message.ToolCalls))
		for _, call := range message.ToolCalls {
			args := strings.TrimSpace(string(call.Args))
			if args == "" || args == "null" {
				lines = append(lines, fmt.Sprintf("tool: %s", call.Name))
				continue
			}
			lines = append(lines, fmt.Sprintf("tool: %s %s", call.Name, compactLine(args, 200)))
		}
		return strings.Join(lines, "\n")
	case "tool":
		if len(message.ToolResults) == 0 {
			return ""
		}
		lines := make([]string, 0, len(message.ToolResults))
		for _, result := range message.ToolResults {
			switch {
			case strings.TrimSpace(result.Error) != "":
				lines = append(lines, "tool error: "+compactLine(result.Error, 200))
			case strings.TrimSpace(result.Content) != "":
				lines = append(lines, compactLine(result.Content, 240))
			default:
				lines = append(lines, "tool result: (no output)")
			}
		}
		return strings.Join(lines, "\n")
	default:
		return ""
	}
}

func (a *App) webSenderName(message msg.Message) string {
	switch message.Role {
	case "user":
		return "You"
	case "system":
		return "System"
	case "tool":
		return "Tool"
	}
	if message.AgentID == "" || message.AgentID == bus.AddrAgentMain {
		return "Mink"
	}
	if a.rt != nil {
		if ident, err := a.rt.GetAgentIdentity(context.Background(), message.AgentID); err == nil && ident.DisplayName != "" {
			return ident.DisplayName
		}
	}
	for _, cfg := range a.cfg.Agents {
		if cfg.ID == message.AgentID && cfg.Name != "" {
			return cfg.Name
		}
	}
	return message.AgentID
}

func (a *App) webDescriptor(message msg.Message) string {
	switch message.Role {
	case "assistant":
		if message.AgentID != "" && message.AgentID != bus.AddrAgentMain {
			return "Agent"
		}
		return "Assistant"
	case "user":
		return "Owner"
	case "system":
		return "System"
	case "tool":
		return "Tool"
	default:
		return ""
	}
}

func (a *App) webUsageMeta(src string) []string {
	var meta []string
	if id, ok := a.sm.CurrentID(src); ok && id != "" {
		meta = append(meta, "session "+compactLine(id, 28))
	}
	if u, ok := a.disp.Usage(src); ok {
		meta = append(meta, fmt.Sprintf("tokens in:%d out:%d", u.Input, u.Output))
	}
	return meta
}

func (a *App) webSessionContextBlocks(src string) []platform.WebContextBlock {
	sess, err := a.sm.Current(src)
	if err != nil || sess == nil {
		return nil
	}
	var blocks []platform.WebContextBlock
	if anchor := sess.LatestAnchor(); anchor != nil {
		blocks = append(blocks, platform.WebContextBlock{
			Title: "Context Anchor",
			Body:  anchor.Summary,
		})
	}
	if prov := sess.Provenance(); prov != nil && prov.ParentSessionID != "" {
		blocks = append(blocks, platform.WebContextBlock{
			Title: "Forked From",
			Body:  prov.ParentSessionID,
		})
	}
	if activity := a.webRunlogSummary(sess.ID(), 18); activity != "" {
		blocks = append(blocks, platform.WebContextBlock{
			Title: "Recent Activity",
			Body:  activity,
		})
	}
	return blocks
}

func (a *App) webRunlogSummary(sessionID string, limit int) string {
	if strings.TrimSpace(sessionID) == "" {
		return ""
	}
	if limit <= 0 {
		limit = 20
	}
	if a.rt != nil {
		if events, err := a.rt.ReplayEventsForSession(context.Background(), sessionID, limit); err == nil && len(events) > 0 {
			out := make([]string, 0, len(events))
			for _, ev := range events {
				if rendered := webReplayLine(ev); rendered != "" {
					out = append(out, rendered)
				}
			}
			return strings.Join(out, "\n")
		}
	}
	return ""
}

func webReplayLine(ev rtsqlite.ReplayEvent) string {
	ts := ""
	if !ev.Timestamp.IsZero() {
		ts = ev.Timestamp.Format("15:04:05")
	}
	step := ""
	if ev.StepNum != nil {
		step = fmt.Sprintf(" step=%d", *ev.StepNum)
	}
	extra := webReplayExtra(ev.Type, ev.Data)
	if extra != "" {
		extra = " " + extra
	}
	switch ev.Type {
	case "user_input", "agent_output", "tool_call", "tool_end", "llm_error", "interrupt", "step_start", "step_end":
		return strings.TrimSpace(fmt.Sprintf("%s %s%s%s", ts, ev.Type, step, extra))
	default:
		return ""
	}
}

func webReplayExtra(kind string, data map[string]any) string {
	if len(data) == 0 {
		return ""
	}
	switch kind {
	case "user_input":
		if v, ok := data["input"].(string); ok {
			return compactLine(v, 120)
		}
	case "agent_output":
		if v, ok := data["content"].(string); ok {
			return compactLine(v, 120)
		}
	case "tool_call":
		name, _ := data["name"].(string)
		if name != "" {
			return name
		}
	case "tool_end":
		name, _ := data["name"].(string)
		if err, ok := data["error"].(string); ok && err != "" {
			if name != "" {
				return name + " error=" + compactLine(err, 120)
			}
			return compactLine(err, 120)
		}
		if name != "" {
			return name
		}
	case "llm_error":
		if err, ok := data["error"].(string); ok {
			return compactLine(err, 120)
		}
	case "interrupt":
		if reason, ok := data["reason"].(string); ok {
			return compactLine(reason, 120)
		}
	}
	return ""
}
