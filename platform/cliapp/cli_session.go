package cliapp

import (
	"fmt"
	"strings"

	"github.com/abcdlsj/mink/msg"
)

func (m *model) currentSessionMessages() []msg.Message {
	if m == nil || m.cli == nil || m.cli.sessionFn == nil {
		return nil
	}
	return m.cli.sessionFn()
}

func (m *model) reloadSessionOutput(previous, current string) {
	lines := []string{styleDim.Render("mink. type 'exit' to quit")}
	lines = append(lines, m.renderSessionTranscript(m.currentSessionMessages())...)
	if previous != "" && current != "" && previous != current {
		lines = append(lines, styleDim.Render(fmt.Sprintf("· session %s → %s", previous, current)))
	}
	m.output = lines
	m.trimOutput(true)
	m.scrollToBottom()
}

func (m *model) renderSessionTranscript(messages []msg.Message) []string {
	lines := make([]string, 0, len(messages)*2)
	for _, message := range messages {
		lines = append(lines, m.renderSessionMessage(message)...)
	}
	return lines
}

func (m *model) renderSessionMessage(message msg.Message) []string {
	switch message.Role {
	case "user":
		return strings.Split(stylePrompt.Render("» ")+message.Content, "\n")
	case "assistant":
		var lines []string
		if strings.TrimSpace(message.Reasoning) != "" {
			lines = append(lines, "")
			lines = append(lines, strings.Split(m.renderThinking(message.Reasoning), "\n")...)
		}
		if strings.TrimSpace(message.Content) != "" {
			lines = append(lines, "")
			lines = append(lines, strings.Split(renderMarkdown(message.Content), "\n")...)
		}
		return lines
	case "system":
		if strings.TrimSpace(message.Content) == "" {
			return nil
		}
		return strings.Split(styleDim.Render(message.Content), "\n")
	default:
		return nil
	}
}
