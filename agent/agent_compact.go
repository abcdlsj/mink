package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/msg"
	"github.com/abcdlsj/mink/session"
)

const (
	compactMaxHistoryRunes = 24000
	compactMaxMessageRunes = 800
)

func (a *Agent) maybeAutoCompact(ctx context.Context, src string) {
	if !a.shouldAutoCompact() {
		return
	}
	if _, err := a.Compact(ctx, src, ""); err != nil {
		a.publishCompactWarning(src, err)
	}
}

func (a *Agent) publishCompactWarning(src string, err error) {
	if err == nil || a.bus == nil {
		return
	}
	a.logWarn("session_compact_warning", map[string]any{"error": err.Error()})
	_ = a.bus.Pub(bus.Msg{
		Type:    bus.TypeAssistant,
		From:    a.id,
		To:      src,
		Payload: fmt.Sprintf("compact warning: %v", err),
	})
}

func (a *Agent) shouldAutoCompact() bool {
	if !a.cfg.Compact.Auto {
		return false
	}
	msgs := a.viewMessages()

	tokenThreshold := a.compactTrigger()
	if total, _ := a.sessionTokenTotal(msgs); total >= tokenThreshold {
		return true
	}

	msgThreshold := a.cfg.Compact.TriggerMessages
	if msgThreshold <= 0 {
		msgThreshold = 80
	}
	return len(msgs) >= msgThreshold
}

func (a *Agent) Compact(ctx context.Context, src, note string) (string, error) {
	view := a.session.View()
	if len(view.Messages) == 0 {
		return "", nil
	}
	msgs := a.session.Messages()

	keepRecent := a.cfg.Compact.KeepRecentMessages
	if keepRecent <= 0 {
		keepRecent = 20
	}
	if len(msgs) > 0 && keepRecent >= len(msgs) && len(view.Messages) == len(msgs) {
		return "", nil
	}

	cut := findCompactCut(msgs, keepRecent)
	recentMsgs := msgs[cut:]
	base := len(view.Messages) - len(msgs)
	if base < 0 {
		base = 0
	}
	history := base + cut
	if history > len(view.Messages) {
		history = len(view.Messages)
	}
	oldMsgs := view.Messages[:history]
	if len(oldMsgs) == 0 {
		return "", nil
	}

	hist := buildCompactHistoryPrompt(oldMsgs, note)
	if strings.TrimSpace(hist) == "" {
		return "", nil
	}

	llmTimeout := time.Duration(a.cfg.Timeout.LLM) * time.Second
	compactCtx := ctx
	if llmTimeout > 0 {
		var cancel context.CancelFunc
		compactCtx, cancel = context.WithTimeout(ctx, llmTimeout)
		defer cancel()
	}

	compactSys := "You produce compact, factual context summaries for coding assistants."
	start := time.Now()
	corrID := a.logLLMRequest(-1, 2, false)
	resp, err := a.p.Chat(compactCtx, []msg.Message{
		{Role: "system", Content: compactSys},
		{Role: "user", Content: hist},
	}, nil)
	if err != nil {
		a.logLLMError(-1, corrID, err, time.Since(start))
		return "", err
	}
	a.logLLMResponse(-1, corrID, resp, time.Since(start))

	summary := strings.TrimSpace(resp.Content)
	if summary == "" {
		summary = "(empty summary)"
	}

	if a.rt != nil {
		if err := a.rt.CompactSource(ctx, src, summary, note); err != nil {
			a.logWarn("session_compact_runtime_error", map[string]any{"error": err.Error()})
			return "", err
		}
	}
	a.session.AddAnchor(session.AnchorSummary, summary, note, cut)
	a.base = tokenBaseline{}
	a.rememberSummary(ctx, src, summary, note)

	oldTotal, _ := a.sessionTokenTotal(view.Messages)
	newView := a.session.View()
	newTotal, _ := a.sessionTokenTotal(newView.Messages)
	msgText := fmt.Sprintf(
		"[Session compacted] anchored %d messages, kept recent %d/%d entries (estimated tokens: %d -> %d)",
		history,
		len(recentMsgs),
		len(msgs),
		oldTotal,
		newTotal,
	)
	if a.bus != nil {
		_ = a.bus.Pub(bus.Msg{
			Type:    bus.TypeSessionCompact,
			From:    a.id,
			To:      src,
			Payload: msgText,
		})
	}

	return msgText, nil
}

func findCompactCut(msgs []msg.Message, keep int) int {
	cut := len(msgs) - keep
	if cut <= 0 {
		return 0
	}

	for cut > 0 && msgs[cut].Role == "tool" {
		id := toolCallID(msgs[cut])
		if id == "" {
			cut++
			break
		}
		if i := findToolCaller(msgs, cut, id); i >= 0 {
			cut = i
		} else {
			cut++
			break
		}
	}
	return cut
}

func toolCallID(m msg.Message) string {
	if len(m.ToolResults) == 0 {
		return ""
	}
	return m.ToolResults[0].ToolCallID
}

func findToolCaller(msgs []msg.Message, end int, id string) int {
	for i := end - 1; i >= 0; i-- {
		if msgs[i].Role != "assistant" || len(msgs[i].ToolCalls) == 0 {
			continue
		}
		for _, tc := range msgs[i].ToolCalls {
			if tc.ID == id {
				return i
			}
		}
	}
	return -1
}

func buildCompactHistoryPrompt(oldMsgs []msg.Message, note string) string {
	var b strings.Builder
	b.WriteString("Summarize the conversation history below for future context retention.\n")
	b.WriteString("Keep goals, decisions, constraints, file paths, pending tasks, and unresolved issues.\n")
	b.WriteString("Be concise and structured with bullet points.\n\n")
	if note != "" {
		b.WriteString("Additional instruction:\n")
		b.WriteString(note)
		b.WriteString("\n\n")
	}
	b.WriteString("History:\n")

	blocks := collectCompactBlocks(oldMsgs, compactMaxHistoryRunes)
	if len(blocks) == 0 {
		return ""
	}
	for _, block := range blocks {
		b.WriteString(block)
	}
	return b.String()
}

func collectCompactBlocks(oldMsgs []msg.Message, maxRunes int) []string {
	if maxRunes <= 0 {
		maxRunes = compactMaxHistoryRunes
	}
	used := 0
	var rev []string

	for i := len(oldMsgs) - 1; i >= 0; i-- {
		block := compactMessageBlock(oldMsgs[i])
		if block == "" {
			continue
		}
		n := len([]rune(block))
		if used+n > maxRunes {
			break
		}
		rev = append(rev, block)
		used += n
	}

	if len(rev) == 0 {
		return nil
	}

	blocks := make([]string, 0, len(rev)+1)
	for i := len(rev) - 1; i >= 0; i-- {
		blocks = append(blocks, rev[i])
	}
	if len(blocks) < len(oldMsgs) {
		blocks = append([]string{"[system]\n(earlier history omitted due to size)\n\n"}, blocks...)
	}
	return blocks
}

func compactMessageBlock(m msg.Message) string {
	content := strings.TrimSpace(m.Content)
	if content == "" {
		if len(m.ToolCalls) > 0 {
			content = fmt.Sprintf("(tool calls: %d)", len(m.ToolCalls))
		} else if len(m.ToolResults) > 0 {
			content = fmt.Sprintf("(tool results: %d)", len(m.ToolResults))
		}
	}
	if content == "" {
		return ""
	}
	if len([]rune(content)) > compactMaxMessageRunes {
		content = string([]rune(content)[:compactMaxMessageRunes]) + "..."
	}
	return fmt.Sprintf("[%s]\n%s\n\n", m.Role, content)
}
