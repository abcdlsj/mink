package web

import (
	"sort"
	"strings"

	"github.com/abcdlsj/mink/msg"
	"github.com/abcdlsj/mink/session"
)

func (s *server) state() (state, error) {
	sessions, err := s.app.ListSessionsBySource(source)
	if err != nil {
		return state{}, err
	}
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})
	current, err := s.app.CurrentSession(source)
	if err != nil {
		return state{}, err
	}
	items := make([]sessionItem, 0, len(sessions))
	for _, sess := range sessions {
		items = append(items, sessionItem{
			ID:      sess.ID,
			Title:   blank(sess.Title, "(untitled)"),
			Updated: sess.UpdatedAt.Format("2006-01-02 15:04"),
			Active:  current != nil && sess.ID == current.ID,
		})
	}
	return state{
		Workspace: s.app.Workspace(),
		Model:     s.app.CurrentModel(),
		Notice:    s.currentNotice(),
		Sessions:  items,
		Current:   currentView(current),
		Messages:  renderMessages(current),
	}, nil
}

func (s *server) currentNotice() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.notice
}

func currentView(current *session.Session) currentState {
	if current == nil {
		return currentState{Title: "(untitled)"}
	}
	return currentState{
		ID:      current.ID,
		Title:   blank(current.Title, "(untitled)"),
		Summary: current.Summary,
		Source:  current.Source,
	}
}

func renderMessages(current *session.Session) []message {
	if current == nil {
		return nil
	}
	msgs := make([]message, 0, len(current.Messages))
	for _, m := range current.Messages {
		msgs = append(msgs, renderMessage(m))
	}
	return msgs
}

func renderMessage(m msg.Message) message {
	out := message{
		Role:      m.Role,
		Content:   m.Content,
		Reasoning: m.Reasoning,
		Time:      m.Timestamp.Format("15:04:05"),
	}
	if len(m.ToolCalls) > 0 {
		calls := make([]toolCall, 0, len(m.ToolCalls))
		for _, tc := range m.ToolCalls {
			calls = append(calls, toolCall{
				Name: tc.Name,
				Args: strings.TrimSpace(string(tc.Args)),
			})
		}
		out.ToolCalls = calls
	}
	if len(m.ToolResults) > 0 {
		results := make([]toolResult, 0, len(m.ToolResults))
		for _, tr := range m.ToolResults {
			results = append(results, toolResult{
				Content: tr.Content,
				Error:   tr.Error,
			})
		}
		out.ToolResults = results
	}
	return out
}

func blank(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return strings.TrimSpace(s)
}
