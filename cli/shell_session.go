package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/abcdlsj/mink/session"
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
	m.session = m.currentSessionIndex(sessions)
	m.overlay = overlaySession
}

func (m *shellModel) currentSessionIndex(sessions []*session.Session) int {
	cur, err := m.app.CurrentSession(m.source)
	if err != nil || cur == nil {
		return 0
	}
	for i, s := range sessions {
		if s != nil && s.ID == cur.ID {
			return i
		}
	}
	return 0
}

func (m *shellModel) handleSessionKey(key string) {
	switch key {
	case "esc", "q":
		m.overlay = overlayNone
	case "j", "down":
		m.moveSession(1)
	case "k", "up":
		m.moveSession(-1)
	case "g", "home":
		m.session = 0
	case "G", "end":
		if len(m.sessions) > 0 {
			m.session = len(m.sessions) - 1
		}
	case "n":
		m.createSession()
	case "enter":
		m.switchSession()
	}
}

func (m *shellModel) moveSession(delta int) {
	if len(m.sessions) == 0 {
		return
	}
	m.session += delta
	if m.session < 0 {
		m.session = len(m.sessions) - 1
	}
	if m.session >= len(m.sessions) {
		m.session = 0
	}
}

func (m *shellModel) switchSession() {
	if len(m.sessions) == 0 || m.session < 0 || m.session >= len(m.sessions) {
		return
	}
	s := m.sessions[m.session]
	if s == nil {
		return
	}
	next, err := m.app.SwitchSession(m.source, s.ID)
	if err != nil {
		m.addTextItem(itemError, err.Error(), time.Now())
		return
	}
	m.overlay = overlayNone
	m.resetTranscript()
	m.addTextItem(itemNotice, "Switched session: "+sessionLabel(next), time.Now())
}

func (m *shellModel) createSession() {
	s, err := m.app.NewSession(m.source)
	if err != nil {
		m.addTextItem(itemError, err.Error(), time.Now())
		return
	}
	m.overlay = overlayNone
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

func sessionLabel(s *session.Session) string {
	if s == nil {
		return "(unknown)"
	}
	if strings.TrimSpace(s.Title) != "" {
		return fmt.Sprintf("%s [%s]", shortID(s.ID), strings.TrimSpace(s.Title))
	}
	return shortID(s.ID)
}
