package cli

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/abcdlsj/sumi/space"
	"github.com/abcdlsj/sumi/textutil"
)

func isChatSelectorCommand(text string) bool {
	text = strings.TrimSpace(text)
	return text == "/chat" || text == "/chats"
}

func isRuntimeSessionCommand(text string) bool {
	fields := strings.Fields(strings.TrimSpace(text))
	return len(fields) > 0 && (fields[0] == "/session" || fields[0] == "/sessions")
}

func (m *shellModel) openChatOverlay() {
	if m.app == nil {
		return
	}
	if m.busy {
		m.addTextItem(itemNotice, "Finish the running turn before switching chats.", time.Now())
		return
	}
	chats, err := listCLISpaces(m.app.Spaces(), "")
	if err != nil {
		m.addTextItem(itemError, err.Error(), time.Now())
		return
	}
	m.chats = chats
	m.chatQuery = ""
	m.chat = m.currentChatIndex()
	m.overlay = overlayChat
}

func (m *shellModel) currentSpace() *space.Space {
	target := space.MapSource(m.source)
	if !space.IsSpaceID(target.Seed) || m.app == nil || m.app.Spaces() == nil {
		return nil
	}
	sp, _ := m.app.Spaces().LoadSpace(target.Seed)
	return sp
}

func (m *shellModel) currentChatIndex() int {
	cur := m.currentSpace()
	if cur == nil {
		return 0
	}
	for i, sp := range m.filteredChats() {
		if sp != nil && sp.ID == cur.ID {
			return i + 1
		}
	}
	return 0
}

func (m *shellModel) handleChatKey(key string) {
	switch key {
	case "esc", "q":
		m.overlay = overlayNone
		m.chatQuery = ""
	case "j", "down":
		m.moveChat(1)
	case "k", "up":
		m.moveChat(-1)
	case "g", "home":
		m.chat = 0
	case "G", "end":
		if n := len(m.chatChoices()); n > 0 {
			m.chat = n - 1
		}
	case "n":
		m.createChat()
	case "enter":
		m.switchChat()
	case "backspace", "ctrl+h":
		if m.chatQuery != "" {
			rs := []rune(m.chatQuery)
			m.chatQuery = string(rs[:len(rs)-1])
			m.chat = 0
		}
	default:
		if r := printableKey(key); r != 0 {
			m.chatQuery += string(r)
			m.chat = 0
		}
	}
}

func (m *shellModel) moveChat(delta int) {
	choices := m.chatChoices()
	if len(choices) == 0 {
		return
	}
	m.chat += delta
	if m.chat < 0 {
		m.chat = len(choices) - 1
	}
	if m.chat >= len(choices) {
		m.chat = 0
	}
}

func (m *shellModel) switchChat() {
	choices := m.chatChoices()
	if len(choices) == 0 || m.chat < 0 || m.chat >= len(choices) {
		return
	}
	choice := choices[m.chat]
	if choice.New {
		m.createChat()
		return
	}
	if choice.Space == nil {
		return
	}
	m.source = cliSpaceSource(choice.Space)
	m.base = m.source
	m.channel = "main"
	m.thread = nil
	_, _ = m.app.CurrentSession(cliSessionSource(choice.Space))
	m.overlay = overlayNone
	m.chatQuery = ""
	m.resetTranscript()
	m.addTextItem(itemNotice, "Switched chat: "+chatLabel(choice.Space), time.Now())
}

func (m *shellModel) createChat() {
	cur := m.currentSpace()
	kind := space.KindDirectChat
	info := space.PersonaInfo{}
	key := newCLIChatKey(kind)
	if cur != nil && cur.Kind == space.KindAgentDM {
		kind = space.KindAgentDM
		key = newCLIChatKey(kind)
		pid := space.AgentParticipantID(cur)
		if p := m.app.Personas().Get(pid); p != nil {
			info = space.PersonaInfo{ID: p.ID, Display: p.Display, Role: p.Description}
		}
	}
	sp, err := m.app.Spaces().DraftSpace(kind, key, "", info)
	if err != nil {
		m.addTextItem(itemError, err.Error(), time.Now())
		return
	}
	m.source = cliSpaceSource(sp)
	m.base = m.source
	m.channel = "main"
	m.thread = nil
	m.app.DraftSession(cliSessionSource(sp))
	m.overlay = overlayNone
	m.chatQuery = ""
	m.resetTranscript()
	m.addTextItem(itemNotice, "New chat: "+chatLabel(sp), time.Now())
}

func (m *shellModel) resetTranscript() {
	m.items = nil
	m.spans = nil
	m.toolItems = map[string]shellToolRef{}
	m.expanded = -1
	m.selected = -1
	m.follow = true
	m.turn = shellTurn{assistantIndex: -1}
	m.spaceID = ""
	m.spaceMsgs = nil
	m.syncViewport()
	m.loadSpaceTranscript()
}

func (m shellModel) filteredChats() []*space.Space {
	q := strings.ToLower(strings.TrimSpace(m.chatQuery))
	if q == "" {
		return m.chats
	}
	out := make([]*space.Space, 0, len(m.chats))
	for _, sp := range m.chats {
		if spaceMatches(sp, q) {
			out = append(out, sp)
		}
	}
	return out
}

type chatChoice struct {
	New   bool
	Space *space.Space
}

func (m shellModel) chatChoices() []chatChoice {
	var out []chatChoice
	if strings.TrimSpace(m.chatQuery) == "" {
		out = append(out, chatChoice{New: true})
	}
	for _, sp := range m.filteredChats() {
		out = append(out, chatChoice{Space: sp})
	}
	return out
}

func (m shellModel) chatItems() []popupItem {
	choices := m.chatChoices()
	items := make([]popupItem, 0, len(choices))
	for _, choice := range choices {
		if choice.New {
			items = append(items, popupItem{Title: "New chat", Meta: "enter", Desc: "Start a clean CLI conversation"})
			continue
		}
		sp := choice.Space
		if sp == nil {
			continue
		}
		items = append(items, popupItem{Title: chatLabel(sp), Meta: spaceTime(sp), Desc: spacePreview(sp)})
	}
	return items
}

func spaceMatches(sp *space.Space, q string) bool {
	if sp == nil {
		return false
	}
	fields := []string{sp.ID, sp.Title}
	for _, m := range sp.Messages {
		fields = append(fields, m.Content, m.Reasoning, m.Error)
	}
	return strings.Contains(strings.ToLower(strings.Join(fields, "\n")), q)
}

func spacePreview(sp *space.Space) string {
	if sp == nil {
		return ""
	}
	for _, m := range sp.Messages {
		if strings.TrimSpace(m.Content) != "" {
			return textutil.Preview(strings.ReplaceAll(m.Content, "\n", " "), 96)
		}
	}
	return ""
}

func spaceTime(sp *space.Space) string {
	if sp == nil {
		return ""
	}
	t := sp.UpdatedAt
	if t.IsZero() {
		t = sp.CreatedAt
	}
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02 15:04")
}

func chatLabel(sp *space.Space) string {
	if sp == nil {
		return "(unknown)"
	}
	if title := strings.TrimSpace(sp.Title); title != "" {
		return fmt.Sprintf("%s [%s]", chatID(sp.ID), title)
	}
	if preview := spacePreview(sp); preview != "" {
		return fmt.Sprintf("%s [%s]", chatID(sp.ID), textutil.Preview(preview, 36))
	}
	return chatID(sp.ID)
}

func printableKey(key string) rune {
	rs := []rune(key)
	if len(rs) != 1 {
		return 0
	}
	r := rs[0]
	if unicode.IsPrint(r) && !unicode.IsControl(r) {
		return r
	}
	return 0
}
