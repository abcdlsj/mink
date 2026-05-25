package cli

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/session"
	"github.com/abcdlsj/sumi/textutil"
)

func isSessionSelectorCommand(text string) bool {
	text = strings.TrimSpace(text)
	return text == "/session" || text == "/sessions"
}

func (m *shellModel) openSessionOverlay() {
	if m.app == nil {
		return
	}
	if m.busy {
		m.addTextItem(itemNotice, "Finish the running turn before switching sessions.", time.Now())
		return
	}
	sessions, err := m.app.ListSessionsBySource(m.source)
	if err != nil {
		m.addTextItem(itemError, err.Error(), time.Now())
		return
	}
	m.sessions = sessions
	m.sessionQuery = ""
	m.session = m.currentSessionIndex()
	m.overlay = overlaySession
}

func (m *shellModel) currentSessionIndex() int {
	cur, err := m.app.CurrentSession(m.source)
	if err != nil || cur == nil {
		return 0
	}
	for i, s := range m.filteredSessions() {
		if s != nil && s.ID == cur.ID {
			return i + 1
		}
	}
	return 0
}

func (m *shellModel) handleSessionKey(key string) {
	switch key {
	case "esc", "q":
		m.overlay = overlayNone
		m.sessionQuery = ""
	case "j", "down":
		m.moveSession(1)
	case "k", "up":
		m.moveSession(-1)
	case "g", "home":
		m.session = 0
	case "G", "end":
		if n := len(m.sessionChoices()); n > 0 {
			m.session = n - 1
		}
	case "n":
		m.createSession()
	case "enter":
		m.switchSession()
	case "backspace", "ctrl+h":
		if m.sessionQuery != "" {
			rs := []rune(m.sessionQuery)
			m.sessionQuery = string(rs[:len(rs)-1])
			m.session = 0
		}
	default:
		if r := printableKey(key); r != 0 {
			m.sessionQuery += string(r)
			m.session = 0
		}
	}
}

func (m *shellModel) moveSession(delta int) {
	choices := m.sessionChoices()
	if len(choices) == 0 {
		return
	}
	m.session += delta
	if m.session < 0 {
		m.session = len(choices) - 1
	}
	if m.session >= len(choices) {
		m.session = 0
	}
}

func (m *shellModel) switchSession() {
	choices := m.sessionChoices()
	if len(choices) == 0 || m.session < 0 || m.session >= len(choices) {
		return
	}
	choice := choices[m.session]
	if choice.New {
		m.createSession()
		return
	}
	s := choice.Session
	if s == nil {
		return
	}
	next, err := m.app.SwitchSession(m.source, s.ID)
	if err != nil {
		m.addTextItem(itemError, err.Error(), time.Now())
		return
	}
	m.overlay = overlayNone
	m.sessionQuery = ""
	m.resetTranscript()
	m.loadTranscript(next)
	m.addTextItem(itemNotice, "Switched session: "+sessionLabel(next), time.Now())
}

func (m *shellModel) createSession() {
	s := m.app.DraftSession(m.source)
	m.overlay = overlayNone
	m.sessionQuery = ""
	m.resetTranscript()
	m.addTextItem(itemNotice, "New session: "+sessionLabel(s), time.Now())
}

func (m *shellModel) resetTranscript() {
	m.items = nil
	m.spans = nil
	m.toolItems = map[string]shellToolRef{}
	m.expanded = -1
	m.selected = -1
	m.follow = true
	m.turn = shellTurn{assistantIndex: -1}
	m.syncViewport()
}

func (m *shellModel) loadTranscript(s *session.Session) {
	if s == nil {
		return
	}
	m.resetTranscript()
	for _, mm := range s.Messages {
		m.addMessage(mm)
	}
	if len(m.items) > 0 {
		m.selected = len(m.items) - 1
		m.follow = true
		m.syncViewport()
	}
}

func (m *shellModel) addMessage(mm msg.Message) {
	t := mm.Timestamp
	switch mm.Role {
	case "user":
		if strings.TrimSpace(mm.Content) != "" {
			m.addTextItem(itemUser, mm.Content, t)
		}
	case "assistant":
		item := chatItem{Kind: itemAssistant, Time: t}
		if strings.TrimSpace(mm.Reasoning) != "" {
			item.Segments = append(item.Segments, chatSegment{Kind: segReasoning, Text: mm.Reasoning, Time: t})
		}
		if strings.TrimSpace(mm.Content) != "" {
			item.Segments = append(item.Segments, chatSegment{Kind: segText, Text: mm.Content, Time: t})
		}
		for _, tc := range mm.ToolCalls {
			item.Segments = append(item.Segments, chatSegment{
				Kind:   segTool,
				Tool:   tc.Name,
				Text:   summarizeToolAction(tc.Name, string(tc.Args)),
				Status: "done",
				Detail: strings.TrimSpace("Tool: " + tc.Name + "\n\nInput:\n" + string(tc.Args)),
				Time:   t,
			})
		}
		if len(item.Segments) > 0 {
			m.addItem(item)
		}
	case "tool":
		if len(m.items) == 0 {
			return
		}
		item := m.items[len(m.items)-1]
		if item == nil || item.Kind != itemAssistant {
			return
		}
		for _, tr := range mm.ToolResults {
			text := textutil.Preview(firstNonEmpty(tr.Content, tr.Error), 88)
			status := "done"
			if strings.TrimSpace(tr.Error) != "" {
				status = "failed"
			}
			item.Segments = append(item.Segments, chatSegment{
				Kind:   segTool,
				Tool:   "tool",
				Text:   text,
				Status: status,
				Detail: strings.TrimSpace(firstNonEmpty(tr.Content, tr.Error)),
				Time:   t,
			})
		}
	}
}

func (m shellModel) filteredSessions() []*session.Session {
	q := strings.ToLower(strings.TrimSpace(m.sessionQuery))
	if q == "" {
		return m.sessions
	}
	out := make([]*session.Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		if sessionMatches(s, q) {
			out = append(out, s)
		}
	}
	return out
}

type sessionChoice struct {
	New     bool
	Session *session.Session
}

func (m shellModel) sessionChoices() []sessionChoice {
	var out []sessionChoice
	if strings.TrimSpace(m.sessionQuery) == "" {
		out = append(out, sessionChoice{New: true})
	}
	for _, s := range m.filteredSessions() {
		out = append(out, sessionChoice{Session: s})
	}
	return out
}

func (m shellModel) sessionItems() []popupItem {
	choices := m.sessionChoices()
	items := make([]popupItem, 0, len(choices))
	for _, choice := range choices {
		if choice.New {
			items = append(items, popupItem{
				Title: "New session",
				Meta:  "enter",
				Desc:  "Start a clean CLI session",
			})
			continue
		}
		s := choice.Session
		if s == nil {
			continue
		}
		items = append(items, popupItem{
			Title: sessionLabel(s),
			Meta:  sessionTime(s),
			Desc:  sessionPreview(s),
		})
	}
	return items
}

func sessionMatches(s *session.Session, q string) bool {
	if s == nil {
		return false
	}
	fields := []string{s.ID, s.Title, s.Summary}
	for _, m := range s.Messages {
		fields = append(fields, m.Content, m.Reasoning)
		for _, tr := range m.ToolResults {
			fields = append(fields, tr.Content, tr.Error)
		}
	}
	hay := strings.ToLower(strings.Join(fields, "\n"))
	return strings.Contains(hay, q)
}

func sessionPreview(s *session.Session) string {
	if s == nil {
		return ""
	}
	for _, m := range s.Messages {
		if strings.TrimSpace(m.Content) != "" {
			return textutil.Preview(strings.ReplaceAll(m.Content, "\n", " "), 96)
		}
	}
	return textutil.Preview(strings.ReplaceAll(s.Summary, "\n", " "), 96)
}

func sessionTime(s *session.Session) string {
	if s == nil {
		return ""
	}
	t := s.UpdatedAt
	if t.IsZero() {
		t = s.CreatedAt
	}
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02 15:04")
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

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func sessionLabel(s *session.Session) string {
	if s == nil {
		return "(unknown)"
	}
	if strings.TrimSpace(s.Title) != "" {
		return fmt.Sprintf("%s [%s]", shortID(s.ID), strings.TrimSpace(s.Title))
	}
	return shortID(s.ID)
}
