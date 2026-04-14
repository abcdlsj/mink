package app

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/platform"
	"github.com/abcdlsj/mink/session"
)

func (a *App) currentSection(src string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if section := a.source(src).section; section != "" {
		return section
	}
	return "main"
}

func (a *App) setActiveSection(src, section string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.source(src).section = strings.TrimSpace(section)
}

func (a *App) currentMainSession(src string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.source(src).sessionID
}

func (a *App) setMainSession(src, sessionID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.source(src).sessionID = strings.TrimSpace(sessionID)
}

func (a *App) webState(ctx context.Context, src string) (platform.WebState, error) {
	state := platform.WebState{
		Workspace: filepath.Base(currentWorkspace()),
		Section:   a.currentSection(src),
		Nav: []platform.WebNavItem{
			{ID: "inbox", Label: "Inbox"},
			{ID: "main", Label: "Main"},
			{ID: "teams", Label: "Teams"},
			{ID: "threads", Label: "Threads"},
		},
	}
	for i := range state.Nav {
		state.Nav[i].Active = state.Nav[i].ID == state.Section
	}

	switch state.Section {
	case "teams":
		return a.webTeamsState(ctx, src, state)
	case "threads":
		return a.webThreadsState(ctx, src, state)
	case "inbox":
		return a.webInboxState(ctx, src, state)
	default:
		return a.webMainState(ctx, src, state)
	}
}

func (a *App) webSelect(ctx context.Context, src, section, id string) error {
	section = strings.TrimSpace(section)
	if section == "" {
		section = "main"
	}
	a.setActiveSection(src, section)

	switch section {
	case "main":
		if id != "" {
			return a.webOpenSession(ctx, src, id)
		}
		if current := a.currentMainSession(src); current != "" {
			return a.webOpenSession(ctx, src, current)
		}
		return a.webNewSession(ctx, src)
	case "teams":
		if id != "" {
			return a.webOpenTeam(ctx, src, id)
		}
		if current := a.currentTeamID(src); current != "" {
			return a.webOpenTeam(ctx, src, current)
		}
		if a.rt == nil {
			return nil
		}
		teams, err := a.rt.ListTeams(ctx, "")
		if err != nil || len(teams) == 0 {
			return err
		}
		return a.webOpenTeam(ctx, src, teams[0].ID)
	case "threads":
		if id != "" {
			return a.webOpenThread(ctx, src, id)
		}
		threads, err := a.webRecentThreads(ctx)
		if err != nil || len(threads) == 0 {
			return err
		}
		return a.webOpenThread(ctx, src, threads[0].Thread.ID)
	case "inbox":
		return nil
	default:
		return fmt.Errorf("unknown section %q", section)
	}
}

func (a *App) webSendMessage(ctx context.Context, src, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if strings.HasPrefix(text, "!") {
		return fmt.Errorf("web composer does not support command messages yet")
	}
	if a.currentSection(src) == "inbox" {
		return fmt.Errorf("select a conversation before sending a message")
	}
	if sess, err := a.sm.Current(src); err == nil && sess != nil && strings.TrimSpace(sess.Status()) == "closed" {
		return fmt.Errorf("active conversation is closed; start a new session or open an active thread")
	}
	if a.rt != nil {
		if threadID := a.currentThreadID(src); threadID != "" {
			thread, err := a.rt.GetThread(ctx, threadID)
			if err != nil {
				return err
			}
			if thread.ID != "" && strings.TrimSpace(thread.Status) == "closed" {
				return fmt.Errorf("thread %s is closed", thread.Title)
			}
		}
	}
	return a.bus.Pub(bus.Msg{
		Type:    bus.TypeUserInput,
		From:    src,
		To:      bus.AddrAgentMain,
		Payload: text,
	})
}

func (a *App) webAction(ctx context.Context, src, name string) error {
	switch strings.TrimSpace(name) {
	case "new_session":
		return a.webNewSession(ctx, src)
	case "close_thread":
		threadID := a.currentThreadID(src)
		if threadID == "" {
			return fmt.Errorf("no active thread")
		}
		_, err := a.closeThread(ctx, src, threadID)
		return err
	default:
		return fmt.Errorf("unknown action %q", name)
	}
}

func (a *App) webNewSession(ctx context.Context, src string) error {
	sessionID, err := a.prepareFreshSource(ctx, src)
	if err != nil {
		return err
	}
	_ = a.sm.Update(sessionID, func(s *session.Session) {
		s.SetKind("main")
		s.SetStatus("active")
	})
	a.setMainSession(src, sessionID)
	a.setActiveSection(src, "main")
	return nil
}

func (a *App) webOpenSession(ctx context.Context, src, sessionID string) error {
	if strings.TrimSpace(sessionID) == "" {
		return nil
	}
	if err := a.sm.RestoreSource(src, sessionID); err != nil {
		return err
	}
	_ = a.sm.Update(sessionID, func(s *session.Session) {
		s.SetKind("main")
		s.SetStatus("active")
	})
	a.disp.InvalidateSource(src)
	a.disp.UnbindTeamSource(src)
	a.setActiveTeam(src, "")
	a.setActiveThread(src, "")
	a.setMainSession(src, sessionID)
	a.setActiveSection(src, "main")
	return nil
}

func (a *App) webOpenTeam(ctx context.Context, src, teamID string) error {
	a.setActiveTeam(src, teamID)
	a.setActiveSection(src, "teams")
	a.setActiveThread(src, "")
	a.disp.UnbindTeamSource(src)
	if a.rt == nil {
		return nil
	}
	threads, err := a.rt.ListThreads(ctx, teamID, "")
	if err != nil || len(threads) == 0 {
		return err
	}
	for _, thread := range threads {
		if thread.Status == "active" {
			return a.webOpenThread(ctx, src, thread.ID)
		}
	}
	return a.webOpenThread(ctx, src, threads[0].ID)
}

func (a *App) webOpenThread(ctx context.Context, src, threadID string) error {
	if a.rt == nil || strings.TrimSpace(threadID) == "" {
		return nil
	}
	thread, err := a.rt.GetThread(ctx, threadID)
	if err != nil {
		return err
	}
	if thread.ID == "" {
		return fmt.Errorf("thread not found")
	}
	if err := a.sm.RestoreSource(src, thread.SessionID); err != nil {
		return err
	}
	status := strings.TrimSpace(thread.Status)
	if status == "" {
		status = "active"
	}
	_ = a.sm.Update(thread.SessionID, func(s *session.Session) {
		s.SetKind("team_thread")
		s.SetStatus(status)
		if strings.TrimSpace(s.Summary()) == "" {
			s.SetSummary(thread.Title)
		}
	})
	a.disp.InvalidateSource(src)
	a.setActiveTeam(src, thread.TeamID)
	a.setActiveThread(src, thread.ID)
	if status == "active" {
		a.disp.BindTeamSource(src, thread.TeamID, thread.ID)
	} else {
		a.disp.UnbindTeamSource(src)
	}
	return nil
}
