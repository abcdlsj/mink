package agent

import (
	"strings"

	"github.com/abcdlsj/mink/llm"
	"github.com/abcdlsj/mink/msg"
)

func (a *Agent) TokenUsage() msg.TokenUsage {
	msgs := a.viewMessages()

	total, source := a.sessionTokenTotal(msgs)
	trigger := a.compactTrigger()
	input := 0
	output := 0
	system := 0
	toolTok := 0

	for _, m := range msgs {
		est := a.estimateMessageTokens(m)
		switch m.Role {
		case "user":
			input += est
		case "assistant":
			output += est
		case "system":
			system += est
		case "tool":
			toolTok += est
		default:
			input += est
		}
	}

	return msg.TokenUsage{
		Messages:       len(msgs),
		Total:          total,
		Input:          input,
		Output:         output,
		System:         system,
		Tool:           toolTok,
		CompactTrigger: trigger,
		ContextWindow:  a.cfg.Active.ContextWindow,
		MaxTokens:      a.maxOutputTokens(),
		Reserve:        a.compactReserveTokens(),
		Source:         source,
	}
}

func (a *Agent) compactTrigger() int {
	trigger := a.cfg.Compact.TriggerTokens
	limit := a.modelCompactLimit()
	if limit > 0 {
		if trigger <= 0 || trigger > limit {
			return limit
		}
		return trigger
	}
	if trigger > 0 {
		return trigger
	}
	return 12000
}

func (a *Agent) modelCompactLimit() int {
	ctxWin := a.cfg.Active.ContextWindow
	if ctxWin <= 0 {
		return 0
	}

	limit := ctxWin - a.maxOutputTokens() - a.compactReserveTokens()
	if limit < 1 {
		return 1
	}
	return limit
}

func (a *Agent) maxOutputTokens() int {
	if a.cfg.Active.MaxTokens > 0 {
		return a.cfg.Active.MaxTokens
	}
	return 4096
}

func (a *Agent) compactReserveTokens() int {
	if a.cfg.Compact.ReserveTokens > 0 {
		return a.cfg.Compact.ReserveTokens
	}
	return 2048
}

func (a *Agent) viewMessages() []msg.Message {
	return a.session.View().Messages
}

func (a *Agent) ensureTokenEstimator() {
	if a.tok == nil {
		a.tok = newTokenEstimator(a.cfg.Active.Model)
		return
	}
	a.tok.setModel(a.cfg.Active.Model)
}

func (a *Agent) estimateTokens(msgs []msg.Message) int {
	a.ensureTokenEstimator()
	return a.tok.messages(msgs)
}

func (a *Agent) estimateMessageTokens(m msg.Message) int {
	a.ensureTokenEstimator()
	return a.tok.message(m)
}

func (a *Agent) sessionTokenTotal(msgs []msg.Message) (int, string) {
	fallback := a.estimateTokens(msgs)
	source := "tiktoken-go"

	if !a.base.valid {
		return fallback, source
	}
	if len(msgs) < a.base.msgCount {
		a.base = tokenBaseline{}
		return fallback, source
	}

	total := a.base.total
	if len(msgs) > a.base.msgCount {
		total += a.estimateTokens(msgs[a.base.msgCount:])
	}
	if total > 0 {
		if a.base.source != "" {
			source = a.base.source + "+tiktoken-go"
		}
		return total, source
	}
	return fallback, source
}

func (a *Agent) updateTokenBaseline(msgs, sysMsgs []msg.Message, usage *llm.TokenUsage) {
	if usage == nil || usage.InputTokens <= 0 {
		return
	}

	input := usage.InputTokens
	if len(sysMsgs) > 0 {
		sysTokens := a.estimateTokens(sysMsgs)
		if sysTokens > 0 && input > sysTokens {
			input -= sysTokens
		}
	}
	if input <= 0 {
		return
	}

	source := strings.TrimSpace(usage.InputSource)
	if source == "" {
		source = "provider.input_tokens"
	}
	a.base = tokenBaseline{
		msgCount: len(msgs),
		total:    input,
		source:   source,
		valid:    true,
	}
}
