package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/abcdlsj/mink/bus"
	rtsqlite "github.com/abcdlsj/mink/runtime/store/sqlite"
	"github.com/abcdlsj/mink/session"
)

func (a *App) sourceFromContext(ctx context.Context) string {
	src := strings.TrimSpace(bus.SourceFrom(ctx))
	if src == "" {
		return a.cliSource()
	}
	return src
}

func (a *App) source(src string) *sourceState {
	s := a.sources[src]
	if s == nil {
		s = &sourceState{}
		a.sources[src] = s
	}
	return s
}

func (a *App) currentTeamID(src string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.source(src).teamID
}

func (a *App) currentThreadID(src string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.source(src).threadID
}

func (a *App) setActiveTeam(src, teamID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.source(src)
	s.teamID = teamID
	if teamID == "" {
		s.threadID = ""
	}
}

func (a *App) setActiveThread(src, threadID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.source(src).threadID = threadID
}

func (a *App) closeThread(ctx context.Context, src, threadID string) (rtsqlite.TeamThread, error) {
	var thread rtsqlite.TeamThread
	if a.rt == nil || strings.TrimSpace(threadID) == "" {
		return thread, nil
	}
	thread, err := a.rt.GetThread(ctx, threadID)
	if err != nil {
		return thread, err
	}
	if thread.ID == "" {
		return thread, fmt.Errorf("thread not found")
	}
	if err := a.rt.UpdateThreadStatus(ctx, thread.ID, "closed"); err != nil {
		return thread, err
	}
	thread.Status = "closed"
	if thread.SessionID != "" {
		_ = a.sm.Update(thread.SessionID, func(s *session.Session) {
			s.SetKind("team_thread")
			s.SetStatus("closed")
			if strings.TrimSpace(s.Summary()) == "" {
				s.SetSummary(thread.Title)
			}
		})
		if sess, err := a.sm.Get(thread.SessionID); err == nil && sess != nil {
			_ = sess.Flush()
		}
	}
	if a.currentThreadID(src) == thread.ID {
		a.setActiveThread(src, "")
		a.disp.UnbindTeamSource(src)
	}
	return thread, nil
}
