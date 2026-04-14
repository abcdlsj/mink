package platform

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/abcdlsj/mink/bus"
)

func (t *Telegram) forward(ctx context.Context) {
	if t.events != nil {
		t.bus.Unobserve(t.events)
	}
	ch := make(chan bus.Msg, 512)
	t.events = ch
	t.bus.Observe(ch)

	for {
		select {
		case m := <-ch:
			t.debugf("bus type=%s from=%s to=%s id=%s", m.Type, m.From, m.To, m.ID)
			if !strings.HasPrefix(m.To, "telegram:") && m.To != bus.AddrBroadcast {
				continue
			}
			t.sendMsg(m)
		case <-t.stop:
			return
		case <-ctx.Done():
			return
		}
	}
}

func (t *Telegram) sendMsg(m bus.Msg) {
	targets := t.targetRoutes(m)
	if len(targets) == 0 {
		return
	}

	prefix := ""
	if m.From != "" && m.From != bus.AddrAgentMain {
		prefix = fmt.Sprintf("%s: ", m.From)
	}

	for _, target := range targets {
		chatID := parseTelegramChatID(target)
		if chatID == 0 {
			continue
		}
		t.touchActive(chatID)
		t.sendToChat(target, chatID, m, prefix)
	}
}

func (t *Telegram) targetRoutes(m bus.Msg) []string {
	if strings.HasPrefix(m.To, "telegram:") {
		return []string{m.To}
	}
	if m.To != bus.AddrBroadcast {
		return nil
	}

	now := time.Now()
	t.activeMu.Lock()
	defer t.activeMu.Unlock()

	routes := make([]string, 0, len(t.activeChats))
	for id, seen := range t.activeChats {
		if now.Sub(seen) > telegramActiveTTL {
			delete(t.activeChats, id)
			continue
		}
		routes = append(routes, bus.Telegram(id))
	}
	return routes
}

func (t *Telegram) sendToChat(route string, chatID int64, m bus.Msg, prefix string) {
	switch m.Type {
	case bus.TypeAssistant:
		raw := fmt.Sprintf("%v", m.Payload)
		out := parseTelegramAssistantOutput(raw)
		if strings.HasPrefix(strings.TrimSpace(out.Text), "[status] ") && out.Reaction == "" {
			status := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(out.Text), "[status] "))
			if status != "" {
				t.setProgress(route, chatID, "[status] "+status)
			}
			return
		}
		t.clearProgress(route)
		replyToID := t.resolveReplyToID(route, out)
		t.debugf("assistant chat=%d reply_to=%d silent=%v react=%q text=%q", chatID, replyToID, out.Silent, out.Reaction, truncateTG(out.Text, 160))
		if out.Reaction != "" {
			t.applyReaction(route, chatID, out.Reaction, replyToID)
		}
		if out.Silent || strings.TrimSpace(out.Text) == "" {
			return
		}
		text := out.Text
		if prefix != "" {
			text = prefix + text
		}
		t.sendAssistantText(route, chatID, text, replyToID)
	case bus.TypeTurnDone:
		t.clearProgress(route)
		t.popInboundState(route)
		t.stopTyping(chatID)
		return
	case bus.TypeToolCall:
		t.handleToolCall(route, chatID)
		return
	case bus.TypeToolResult:
		t.handleToolResult(route, chatID)
		return
	case bus.TypeToolError:
		t.handleToolError(route, chatID)
	case bus.TypeAgentSpawn:
		if payload, ok := m.Payload.(map[string]string); ok {
			task := truncateTG(payload["task"], 100)
			t.sendText(chatID, fmt.Sprintf("spawned %s: %s", payload["agent_id"], task))
		}
	case bus.TypeAgentDone:
		if payload, ok := m.Payload.(map[string]string); ok {
			t.sendText(chatID, fmt.Sprintf("completed %s: %s", payload["agent_id"], payload["result"]))
		}
	case bus.TypeTaskStart:
		if payload, ok := m.Payload.(map[string]string); ok {
			cmd := truncateTG(payload["cmd"], 50)
			t.sendText(chatID, fmt.Sprintf("task start %s: %s", payload["task_id"], cmd))
		}
	case bus.TypeTaskDone:
		if payload, ok := m.Payload.(map[string]string); ok {
			if payload["status"] == "ok" {
				t.sendText(chatID, fmt.Sprintf("task done %s", payload["task_id"]))
			} else {
				t.sendText(chatID, fmt.Sprintf("task error %s: %s", payload["task_id"], truncateTG(payload["error"], 50)))
			}
		}
	case bus.TypeStreamChunk:
		t.handleStreamChunk(route, chatID, fmt.Sprintf("%v", m.Payload))
	case bus.TypeStreamEnd:
		t.handleStreamEnd(route)
		t.stopTyping(chatID)
	case bus.TypeSessionReset:
		t.clearProgress(route)
		t.stopTyping(chatID)
	case bus.TypeInterrupt:
		t.clearProgress(route)
		t.stopTyping(chatID)
	case bus.TypeSessionNew:
		if id, ok := m.Payload.(string); ok {
			t.sendText(chatID, fmt.Sprintf("session: %s", id))
		}
	case bus.TypeSessionCompact:
		t.sendText(chatID, fmt.Sprintf("session compact: %v", m.Payload))
	}
}
