package platform

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/abcdlsj/mink/bus"
	"github.com/muesli/reflow/wordwrap"
)

func (m *model) handleBusMsg(msg bus.Msg) (tea.Model, tea.Cmd) {
	if msg.To != bus.AddrBroadcast && msg.To != bus.AddrPlatformCLI {
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
			m.appendOutput(styleSession.Render(fmt.Sprintf("[Session] %s", id)))
		}

	case bus.TypeSessionCompact:
		m.appendOutput(styleTool.Render(fmt.Sprintf("[Compact] %v", msg.Payload)))
	}

	return m, nil
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

func (m *model) handleAssistantMsg(msg bus.Msg, showInline bool) {
	content := fmt.Sprintf("%v", msg.Payload)
	if content == "busy" && m.pending > 0 {
		m.pending--
		m.appendOutput(styleDim.Render("[busy] waiting for previous request..."))
		return
	}
	if showInline {
		m.appendOutput("")
		m.appendOutput(renderMarkdown(content))
		return
	}
	m.appendToAgent(msg.From, renderMarkdown(truncate(content, 160)))
}

func (m *model) handleToolCallMsg(msg bus.Msg, showInline bool) tea.Cmd {
	payload, ok := msg.Payload.(map[string]string)
	if !ok {
		m.appendBusLine(showInline, msg.From, styleTool.Render("◉ "+truncate(fmt.Sprintf("%v", msg.Payload), 120)))
		return nil
	}

	id := payload["id"]
	name := payload["name"]
	args := payload["args"]
	display := truncate(name+" "+args, 100)
	if !showInline {
		m.appendToAgent(msg.From, styleTool.Render("◉ "+display))
		return nil
	}

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
	return s.Tick
}

func (m *model) handleToolResultMsg(msg bus.Msg, showInline bool) {
	payload, ok := msg.Payload.(map[string]string)
	if !ok {
		m.appendBusLine(showInline, msg.From, styleSuccess.Render("✓ done"))
		return
	}

	id := payload["id"]
	if ts, exists := m.tools[id]; exists && !ts.done {
		ts.done = true
		if ts.line >= 0 && ts.line < len(m.output) {
			display := truncate(ts.name+" "+ts.args, 100)
			m.output[ts.line] = styleSuccess.Render("✓") + " " + styleTool.Render(display)
		}
		return
	}
	if !showInline {
		m.appendToAgent(msg.From, styleSuccess.Render("✓ done"))
	}
}

func (m *model) handleToolErrorMsg(msg bus.Msg, showInline bool) {
	payload, ok := msg.Payload.(map[string]string)
	if !ok {
		m.appendBusLine(showInline, msg.From, styleFail.Render("✗ "+truncate(fmt.Sprintf("%v", msg.Payload), 160)))
		return
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
		return
	}
	if !showInline {
		m.appendToAgent(msg.From, styleFail.Render("✗ "+truncate(errMsg, 160)))
	}
}

func (m *model) resetTurnState() {
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

func (m *model) handleTaskStartMsg(msg bus.Msg) {
	payload, ok := msg.Payload.(map[string]string)
	if !ok {
		return
	}
	taskID := payload["task_id"]
	cmdText := truncate(payload["cmd"], 60)
	m.appendOutput(styleTool.Render(fmt.Sprintf("[%s] %s", taskID, cmdText)))
}

func (m *model) handleTaskDoneMsg(msg bus.Msg) {
	payload, ok := msg.Payload.(map[string]string)
	if !ok {
		return
	}
	taskID := payload["task_id"]
	if payload["status"] == "ok" {
		m.appendOutput(styleSuccess.Render(fmt.Sprintf("✓ [%s] completed", taskID)))
		return
	}
	errMsg := truncate(payload["error"], 80)
	m.appendOutput(styleFail.Render(fmt.Sprintf("✗ [%s] %s", taskID, errMsg)))
}

func (m *model) handleStreamChunkMsg(msg bus.Msg, showInline bool) {
	if !showInline {
		return
	}
	delta, _ := msg.Payload.(string)
	if delta == "" {
		return
	}
	if m.streaming {
		m.streamBuf.WriteString(delta)
		m.replaceFrom(m.streamStart, renderMarkdown(m.streamBuf.String()))
		return
	}
	m.streaming = true
	m.streamBuf.Reset()
	m.streamBuf.WriteString(delta)
	m.appendOutput("")
	m.streamStart = len(m.output)
	m.replaceFrom(m.streamStart, renderMarkdown(delta))
}

func (m *model) handleThinkingChunkMsg(msg bus.Msg, showInline bool) {
	if !showInline {
		return
	}
	delta, _ := msg.Payload.(string)
	if delta == "" {
		return
	}
	if m.thinking {
		m.thinkingBuf.WriteString(delta)
		m.replaceFrom(m.thinkStart, m.renderThinking(m.thinkingBuf.String()))
		return
	}
	m.thinking = true
	m.thinkingBuf.Reset()
	m.thinkingBuf.WriteString(delta)
	m.appendOutput("")
	m.thinkStart = len(m.output)
	m.replaceFrom(m.thinkStart, m.renderThinking(delta))
}

func (m *model) renderThinking(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	width := m.width
	if width <= 4 {
		width = 80
	}
	w := wordwrap.NewWriter(width)
	w.Breakpoints = []rune{' ', '-', '_', '.', ',', ':', ';', '!', '?', '(', ')', '[', ']', '{', '}'}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		wrapped, _ := w.Write([]byte(line))
		lines[i] = styleThinking.Render(string(wrapped))
	}
	return strings.Join(lines, "\n")
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
