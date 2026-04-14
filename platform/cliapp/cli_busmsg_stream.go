package cliapp

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/muesli/reflow/wordwrap"

	"github.com/abcdlsj/mink/bus"
)

func (m *model) handleAssistantMsg(msg bus.Msg, showInline bool) {
	content := fmt.Sprintf("%v", msg.Payload)
	if content == "busy" {
		m.appendOutput(styleDim.Render("· busy"))
		return
	}
	if strings.HasPrefix(content, "[status] ") {
		status := strings.TrimPrefix(content, "[status] ")
		m.appendBusLine(showInline, msg.From, styleDim.Render("· "+status))
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
		agentID: msg.From,
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
		m.recordToolLog(styleSuccess.Render("✓ " + truncate(ts.name+" "+ts.args, 80)))
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
		m.recordToolLog(styleFail.Render("✗ " + truncate(ts.name+" "+ts.args, 48) + " " + truncate(errMsg, 28)))
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
	m.clearTurnState()
}

func (m *model) clearSessionState() {
	m.pending = 0
	m.clearTurnState()
}

func (m *model) clearTurnState() {
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
	width := m.mainPaneWidth()
	if width <= 4 {
		width = 80
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		w := wordwrap.NewWriter(width)
		w.Breakpoints = []rune{' ', '-', '_', '.', ',', ':', ';', '!', '?', '(', ')', '[', ']', '{', '}'}
		w.Write([]byte(line))
		w.Close()
		content := w.String()
		if i == 0 {
			content = styleThinkingTag.Render("thinking: ") + styleThinking.Render(content)
		} else {
			content = styleThinking.Render(content)
		}
		lines[i] = content
	}
	return strings.Join(lines, "\n")
}
