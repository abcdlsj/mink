package cli

import (
	"fmt"
	"strings"

	"github.com/abcdlsj/sumi/textutil"
)

func (m *shellModel) runNavCommand(text string) (string, bool) {
	cmd, args, ok := parseNavCommand(text)
	if !ok {
		return "", false
	}
	switch cmd {
	case "main":
		return m.switchChannel("main"), true
	case "channel", "ch":
		return m.runChannelCommand(args), true
	case "thread", "th":
		return m.runThreadCommand(args), true
	case "threads":
		return m.listThreads(), true
	default:
		return "", false
	}
}

func parseNavCommand(text string) (cmd string, args []string, ok bool) {
	s := strings.TrimSpace(text)
	if !strings.HasPrefix(s, "/") {
		return "", nil, false
	}
	parts := strings.Fields(strings.TrimPrefix(s, "/"))
	if len(parts) == 0 {
		return "", nil, false
	}
	return parts[0], parts[1:], true
}

func (m *shellModel) runChannelCommand(args []string) string {
	if len(args) == 0 {
		return fmt.Sprintf("channel: %s", m.channelLabel())
	}
	switch args[0] {
	case "new", "switch":
		if len(args) < 2 {
			return "usage: /channel new <name>"
		}
		return m.switchChannel(args[1])
	case "main":
		return m.switchChannel("main")
	default:
		return m.switchChannel(args[0])
	}
}

func (m *shellModel) runThreadCommand(args []string) string {
	if len(args) == 0 {
		if m.thread == nil {
			return "thread: none"
		}
		return fmt.Sprintf("thread: %s [%s]", m.thread.Title, m.thread.ID)
	}
	switch args[0] {
	case "new":
		title := strings.TrimSpace(strings.Join(args[1:], " "))
		th := m.createThread(title, "")
		m.enterThread(th)
		return "thread: " + m.threadLabel()
	case "leave", "exit":
		if m.thread == nil {
			return "already in channel"
		}
		m.leaveThread()
		return "left thread"
	case "last":
		id := m.lastMessageID()
		if id == "" {
			return "no message to thread"
		}
		return m.openThread(id)
	default:
		return m.openThread(args[0])
	}
}

func (m *shellModel) switchChannel(name string) string {
	raw := strings.TrimSpace(name)
	name = cleanSpaceName(raw)
	if raw == "" || raw == "main" {
		name = "main"
	}
	if name == "" {
		return "usage: /channel <name>"
	}
	if m.busy {
		return "Finish the running turn before switching channels."
	}
	m.channel = name
	m.thread = nil
	m.source = m.channelSource(name)
	m.resetTranscript()
	return "channel: " + m.channelLabel()
}

func (m *shellModel) enterThread(th shellThread) {
	m.thread = &th
	m.source = th.Source
	m.resetTranscript()
}

func (m *shellModel) leaveThread() {
	m.thread = nil
	m.source = m.channelSource(m.channel)
	m.resetTranscript()
}

func (m *shellModel) openThread(id string) string {
	id = cleanSpaceName(id)
	if id == "" {
		return "usage: /thread <id|name>"
	}
	if m.busy {
		return "Finish the running turn before switching threads."
	}
	if th, ok := m.findThread(id); ok {
		m.enterThread(th)
		return "thread: " + m.threadLabel()
	}
	if item := m.findItem(id); item != nil {
		title := threadTitle(item)
		th := m.createThread(title, item.ID)
		m.enterThread(th)
		return "thread: " + m.threadLabel()
	}
	return "thread not found: " + id
}

func (m *shellModel) createThread(title, id string) shellThread {
	id = cleanSpaceName(id)
	if id == "" {
		id = m.nextItemID()
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = "thread-" + id
	}
	th := shellThread{
		ID:     id,
		Title:  title,
		Source: m.channelSource(m.channel) + ":thread:" + id,
	}
	key := m.channelKey()
	m.threads[key] = append(m.threads[key], th)
	return th
}

func (m *shellModel) listThreads() string {
	threads := m.threads[m.channelKey()]
	if len(threads) == 0 {
		return "no threads"
	}
	var lines []string
	for _, th := range threads {
		lines = append(lines, fmt.Sprintf("%s  %s", th.ID, th.Title))
	}
	return strings.Join(lines, "\n")
}

func (m *shellModel) findThread(q string) (shellThread, bool) {
	q = strings.TrimSpace(q)
	for _, th := range m.threads[m.channelKey()] {
		if th.ID == q || cleanSpaceName(th.Title) == q {
			return th, true
		}
	}
	return shellThread{}, false
}

func (m *shellModel) findItem(id string) *chatItem {
	for _, item := range m.items {
		if item != nil && item.ID == id {
			return item
		}
	}
	return nil
}

func (m *shellModel) lastMessageID() string {
	for i := len(m.items) - 1; i >= 0; i-- {
		if m.items[i] != nil && m.items[i].ID != "" {
			return m.items[i].ID
		}
	}
	return ""
}

func (m shellModel) channelSource(name string) string {
	base := strings.TrimSpace(m.base)
	if base == "" {
		base = "cli"
	}
	name = cleanSpaceName(name)
	if name == "" || name == "main" {
		return base
	}
	return base + ":channel:" + name
}

func (m shellModel) channelKey() string {
	if key := cleanSpaceName(m.channel); key != "" {
		return key
	}
	return "main"
}

func (m shellModel) channelLabel() string {
	if m.channel == "" || m.channel == "main" {
		return "#main"
	}
	return "#" + m.channel
}

func (m shellModel) threadLabel() string {
	if m.thread == nil {
		return ""
	}
	return fmt.Sprintf("%s [%s]", m.thread.Title, m.thread.ID)
}

func threadTitle(item *chatItem) string {
	text := strings.TrimSpace(itemText(item))
	if text == "" {
		text = item.detailText()
	}
	if text == "" {
		text = item.ID
	}
	return textutil.Preview(text, 32)
}

func cleanSpaceName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
			lastDash = false
		case r == '-' && !lastDash:
			b.WriteByte('-')
			lastDash = true
		}
		if b.Len() >= 32 {
			break
		}
	}
	return strings.Trim(b.String(), "-")
}
