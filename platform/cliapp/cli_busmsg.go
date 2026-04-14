package cliapp

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/abcdlsj/mink/bus"
)

func (m *model) handleBusMsg(msg bus.Msg) (tea.Model, tea.Cmd) {
	if !m.isRelevantMsg(msg) {
		return m, nil
	}

	showInline := m.shouldShowInline(msg.From)

	switch msg.Type {
	case bus.TypeAgentSpawn:
		return m.handleAgentSpawn(msg)

	case bus.TypeAgentDone:
		return m.handleAgentDone(msg)

	case bus.TypeAssistant:
		m.handleAssistantMsg(msg, showInline)

	case bus.TypeToolCall:
		if cmd := m.handleToolCallMsg(msg, showInline); cmd != nil {
			return m, cmd
		}

	case bus.TypeToolResult:
		m.handleToolResultMsg(msg, showInline)

	case bus.TypeToolError:
		m.handleToolErrorMsg(msg, showInline)

	case bus.TypeTurnDone:
		if msg.From == bus.AddrAgentMain {
			m.resetTurnState()
		}

	case bus.TypeTaskStart:
		m.handleTaskStartMsg(msg)

	case bus.TypeTaskDone:
		m.handleTaskDoneMsg(msg)

	case bus.TypeDelegate:
		m.handleDelegateMsg(msg)

	case bus.TypeDelegateAck:
		m.handleDelegateAckMsg(msg)

	case bus.TypeDelegateResult:
		m.handleDelegateResultMsg(msg)

	case bus.TypeStreamChunk:
		m.handleStreamChunkMsg(msg, showInline)

	case bus.TypeStreamEnd:
		if !showInline {
			break
		}
		m.streaming = false
		m.streamBuf.Reset()

	case bus.TypeThinkingChunk:
		m.handleThinkingChunkMsg(msg, showInline)

	case bus.TypeThinkingEnd:
		if !showInline {
			break
		}
		m.thinking = false
		m.thinkingBuf.Reset()

	case bus.TypeSessionNew:
		if id, ok := msg.Payload.(string); ok {
			if m.lastSession == "" {
				m.lastSession = id
				if len(m.currentSessionMessages()) > 0 {
					m.clearSessionState()
					m.reloadSessionOutput("", id)
				}
			} else if id != m.lastSession {
				prev := m.lastSession
				m.lastSession = id
				m.clearSessionState()
				m.reloadSessionOutput(prev, id)
			}
		}

	case bus.TypeSessionCompact:
		m.appendOutput(styleDim.Render("· context compacted"))
	}

	return m, nil
}

func (m *model) isRelevantMsg(msg bus.Msg) bool {
	src := bus.AddrPlatformCLI
	if m.cli != nil {
		src = m.cli.Source()
	}
	if msg.To == bus.AddrBroadcast || msg.To == src {
		return true
	}
	switch msg.Type {
	case bus.TypeDelegate, bus.TypeDelegateAck, bus.TypeDelegateResult:
		return true
	default:
		return false
	}
}

func (m *model) shouldShowInline(from string) bool {
	isSubAgent := from != "" && from != bus.AddrAgentMain && from != bus.AddrSystemSup
	return !isSubAgent || m.isDirectOutput(from)
}

func (m *model) appendBusLine(showInline bool, from, line string) {
	if showInline {
		m.appendOutput(line)
		return
	}
	m.appendToAgent(from, line)
}
