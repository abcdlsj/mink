package platform

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/abcdlsj/mink/bus"
)

func (m *model) handleBusMsg(msg bus.Msg) (tea.Model, tea.Cmd) {
	if msg.To != bus.AddrBroadcast && msg.To != bus.AddrPlatformCLI {
		return m, nil
	}

	isSubAgent := msg.From != "" && msg.From != bus.AddrAgentMain && msg.From != bus.AddrSystemSup
	showInline := !isSubAgent || m.isDirectOutput(msg.From)

	switch msg.Type {
	case bus.TypeAgentSpawn:
		return m.handleAgentSpawn(msg)

	case bus.TypeAgentDone:
		return m.handleAgentDone(msg)

	case bus.TypeAssistant:
		if showInline {
			m.appendOutput("")
			m.appendOutput(renderMarkdown(fmt.Sprintf("%v", msg.Payload)))
		} else {
			m.appendToAgent(msg.From, renderMarkdown(truncate(fmt.Sprintf("%v", msg.Payload), 160)))
		}

	case bus.TypeToolCall:
		payload, ok := msg.Payload.(map[string]string)
		if !ok {
			line := styleTool.Render("◉ " + truncate(fmt.Sprintf("%v", msg.Payload), 120))
			if showInline {
				m.appendOutput(line)
			} else {
				m.appendToAgent(msg.From, line)
			}
			break
		}
		id := payload["id"]
		name := payload["name"]
		args := payload["args"]
		display := truncate(name+" "+args, 100)

		if showInline {
			s := spinner.New()
			s.Spinner = spinner.Dot
			s.Style = styleTool
			lineIdx := len(m.output)
			m.appendOutput(s.View() + " " + styleTool.Render(display))
			m.tools[id] = &toolState{
				id:      id,
				name:    name,
				args:    args,
				line:    lineIdx,
				spinner: s,
			}
			return m, s.Tick
		} else {
			m.appendToAgent(msg.From, styleTool.Render("◉ "+display))
		}

	case bus.TypeToolResult:
		payload, ok := msg.Payload.(map[string]string)
		if !ok {
			if showInline {
				m.appendOutput(styleSuccess.Render("✓ done"))
			} else {
				m.appendToAgent(msg.From, styleSuccess.Render("✓ done"))
			}
			break
		}
		id := payload["id"]
		if ts, exists := m.tools[id]; exists && !ts.done {
			ts.done = true
			if ts.line >= 0 && ts.line < len(m.output) {
				display := truncate(ts.name+" "+ts.args, 100)
				m.output[ts.line] = styleSuccess.Render("✓") + " " + styleTool.Render(display)
			}
		} else if !showInline {
			m.appendToAgent(msg.From, styleSuccess.Render("✓ done"))
		}

	case bus.TypeToolError:
		payload, ok := msg.Payload.(map[string]string)
		if !ok {
			line := styleFail.Render("✗ " + truncate(fmt.Sprintf("%v", msg.Payload), 160))
			if showInline {
				m.appendOutput(line)
			} else {
				m.appendToAgent(msg.From, line)
			}
			break
		}
		id := payload["id"]
		errMsg := payload["error"]
		if ts, exists := m.tools[id]; exists && !ts.done {
			ts.done = true
			ts.err = errMsg
			if ts.line >= 0 && ts.line < len(m.output) {
				display := truncate(ts.name+" "+ts.args, 80)
				m.output[ts.line] = styleFail.Render("✗") + " " + styleTool.Render(display) + " " + styleFail.Render(truncate(errMsg, 40))
			}
		} else if !showInline {
			m.appendToAgent(msg.From, styleFail.Render("✗ "+truncate(errMsg, 160)))
		}

	case bus.TypeTurnDone:
		if msg.From == bus.AddrAgentMain {
			if m.pending > 0 {
				m.pending--
			}
			m.agentKeys = []string{}
			m.agents = make(map[string]*agentState)
			m.tools = make(map[string]*toolState)
			m.streaming = false
			m.streamBuf.Reset()
			m.streamStart = 0
			m.thinking = false
			m.thinkingBuf.Reset()
			m.thinkStart = 0
		}

	case bus.TypeTaskStart:
		if payload, ok := msg.Payload.(map[string]string); ok {
			taskID := payload["task_id"]
			cmdText := truncate(payload["cmd"], 60)
			m.appendOutput(styleTool.Render(fmt.Sprintf("[%s] %s", taskID, cmdText)))
		}

	case bus.TypeTaskDone:
		if payload, ok := msg.Payload.(map[string]string); ok {
			taskID := payload["task_id"]
			status := payload["status"]
			if status == "ok" {
				m.appendOutput(styleSuccess.Render(fmt.Sprintf("✓ [%s] completed", taskID)))
			} else {
				errMsg := truncate(payload["error"], 80)
				m.appendOutput(styleFail.Render(fmt.Sprintf("✗ [%s] %s", taskID, errMsg)))
			}
		}

	case bus.TypeStreamChunk:
		if !showInline {
			break
		}
		delta, _ := msg.Payload.(string)
		if delta == "" {
			break
		}
		if m.streaming {
			m.streamBuf.WriteString(delta)
			m.replaceFrom(m.streamStart, renderMarkdown(m.streamBuf.String()))
		} else {
			m.streaming = true
			m.streamBuf.Reset()
			m.streamBuf.WriteString(delta)
			m.appendOutput("")
			m.streamStart = len(m.output)
			m.replaceFrom(m.streamStart, renderMarkdown(delta))
		}

	case bus.TypeStreamEnd:
		if !showInline {
			break
		}
		m.streaming = false
		m.streamBuf.Reset()

	case bus.TypeThinkingChunk:
		if !showInline {
			break
		}
		delta, _ := msg.Payload.(string)
		if delta == "" {
			break
		}
		thinkLine := func(s string) string {
			return styleThinking.Render("thinking: " + strings.ReplaceAll(s, "\n", " "))
		}
		if m.thinking {
			m.thinkingBuf.WriteString(delta)
			m.replaceFrom(m.thinkStart, thinkLine(m.thinkingBuf.String()))
		} else {
			m.thinking = true
			m.thinkingBuf.Reset()
			m.thinkingBuf.WriteString(delta)
			m.appendOutput("")
			m.thinkStart = len(m.output)
			m.replaceFrom(m.thinkStart, thinkLine(delta))
		}

	case bus.TypeThinkingEnd:
		if !showInline {
			break
		}
		m.thinking = false
		m.thinkingBuf.Reset()

	case bus.TypeSessionNew:
		if id, ok := msg.Payload.(string); ok {
			m.appendOutput(styleSession.Render(fmt.Sprintf("[Session] %s", id)))
		}

	case bus.TypeSessionCompact:
		m.appendOutput(styleTool.Render(fmt.Sprintf("[Compact] %v", msg.Payload)))
	}

	return m, nil
}

func (m *model) handleAgentSpawn(msg bus.Msg) (tea.Model, tea.Cmd) {
	payload, ok := msg.Payload.(map[string]string)
	if !ok {
		return m, nil
	}

	id := payload["agent_id"]
	if id == "" {
		id = msg.From
	}
	task := truncate(payload["task"], 60)

	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = styleAgent

	if _, exists := m.agents[id]; !exists {
		m.agentKeys = append(m.agentKeys, id)
	}
	m.agents[id] = &agentState{
		id:           id,
		task:         task,
		lines:        []string{},
		directOutput: payload["direct_output"] == "true",
		spinner:      s,
	}

	return m, m.agents[id].spinner.Tick
}

func (m *model) handleAgentDone(msg bus.Msg) (tea.Model, tea.Cmd) {
	payload, ok := msg.Payload.(map[string]string)
	if !ok {
		return m, nil
	}

	id := payload["agent_id"]
	if id == "" {
		id = msg.From
	}

	if agent, exists := m.agents[id]; exists {
		agent.done = true
	}

	return m, nil
}
