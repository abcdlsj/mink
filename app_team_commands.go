package mink

import (
	"context"
	"fmt"
	"strings"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/session"
)

func (a *App) runTeamCommand(ctx context.Context, args []string) (string, error) {
	if a.rt == nil {
		return "runtime db not available", nil
	}
	src := a.sourceFromContext(ctx)
	if len(args) == 0 {
		return "usage: !team [list|create <name>|open <id>|home|policy <leader_driven|round_robin>|invite <agent_id> [role]]", nil
	}

	switch args[0] {
	case "list":
		return a.renderTeamList(ctx, src)
	case "create":
		name := strings.TrimSpace(strings.Join(args[1:], " "))
		if name == "" {
			return "usage: !team create <name>", nil
		}
		teamID, err := a.rt.CreateTeam(ctx, name, bus.AddrAgentMain, "", 0)
		if err != nil {
			return "", err
		}
		a.setActiveTeam(src, teamID)
		a.setActiveThread(src, "")
		a.disp.UnbindTeamSource(src)
		return fmt.Sprintf("created team %s (%s)", name, teamID), nil
	case "policy":
		if len(args) < 2 {
			return "usage: !team policy <leader_driven|round_robin>", nil
		}
		teamID := a.currentTeamID(src)
		if teamID == "" {
			return "no active team", nil
		}
		policy := strings.TrimSpace(args[1])
		if policy != "leader_driven" && policy != "round_robin" {
			return "usage: !team policy <leader_driven|round_robin>", nil
		}
		if err := a.rt.UpdateTeamTurnPolicy(ctx, teamID, policy); err != nil {
			return "", err
		}
		return fmt.Sprintf("updated team %s turn policy to %s", teamID, policy), nil
	case "open":
		if len(args) < 2 {
			return "usage: !team open <team_id>", nil
		}
		team, err := a.rt.GetTeam(ctx, args[1])
		if err != nil {
			return "", err
		}
		if team.ID == "" {
			return "team not found", nil
		}
		a.setActiveTeam(src, team.ID)
		a.setActiveThread(src, "")
		a.disp.UnbindTeamSource(src)
		return fmt.Sprintf("opened team %s (%s)", team.Name, team.ID), nil
	case "home":
		teamID := a.currentTeamID(src)
		if teamID == "" {
			return "no active team", nil
		}
		a.setActiveThread(src, "")
		a.disp.UnbindTeamSource(src)
		team, err := a.rt.GetTeam(ctx, teamID)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("showing team home: %s (%s)", team.Name, team.ID), nil
	case "invite":
		if len(args) < 2 {
			return "usage: !team invite <agent_id> [role]", nil
		}
		teamID := a.currentTeamID(src)
		if teamID == "" {
			return "no active team", nil
		}
		agentID := args[1]
		role := strings.TrimSpace(strings.Join(args[2:], " "))
		if role == "" {
			role = agentID
		}
		if err := a.rt.AddTeamMember(ctx, teamID, agentID, role, role, "persistent"); err != nil {
			return "", err
		}
		return fmt.Sprintf("invited %s to team %s as %s", agentID, teamID, role), nil
	default:
		return "usage: !team [list|create <name>|open <id>|home|policy <leader_driven|round_robin>|invite <agent_id> [role]]", nil
	}
}

func (a *App) runThreadCommand(ctx context.Context, args []string) (string, error) {
	if a.rt == nil {
		return "runtime db not available", nil
	}
	src := a.sourceFromContext(ctx)
	if len(args) == 0 {
		return "usage: !thread [list|new <title>|open <thread_id>|close [thread_id]]", nil
	}

	switch args[0] {
	case "list":
		return a.renderThreadList(ctx, src)
	case "new":
		teamID := a.currentTeamID(src)
		if teamID == "" {
			return "no active team", nil
		}
		title := strings.TrimSpace(strings.Join(args[1:], " "))
		if title == "" {
			return "usage: !thread new <title>", nil
		}
		sess, err := a.sm.ResetSource(src)
		if err != nil {
			return "", err
		}
		sess.SetKind("team_thread")
		sess.SetStatus("active")
		sess.SetSummary(title)
		a.disp.InvalidateSource(src)
		threadID, err := a.rt.CreateThread(ctx, teamID, title, sess.ID())
		if err != nil {
			return "", err
		}
		a.setActiveThread(src, threadID)
		a.disp.BindTeamSource(src, teamID, threadID)
		return fmt.Sprintf("created thread %s (%s)", title, threadID), nil
	case "open":
		if len(args) < 2 {
			return "usage: !thread open <thread_id>", nil
		}
		thread, err := a.rt.GetThread(ctx, args[1])
		if err != nil {
			return "", err
		}
		if thread.ID == "" {
			return "thread not found", nil
		}
		if err := a.sm.RestoreSource(src, thread.SessionID); err != nil {
			return "", err
		}
		_ = a.sm.Update(thread.SessionID, func(s *session.Session) {
			s.SetKind("team_thread")
			s.SetStatus("active")
			if strings.TrimSpace(s.Summary()) == "" {
				s.SetSummary(thread.Title)
			}
		})
		a.disp.InvalidateSource(src)
		a.setActiveTeam(src, thread.TeamID)
		a.setActiveThread(src, thread.ID)
		a.disp.BindTeamSource(src, thread.TeamID, thread.ID)
		return fmt.Sprintf("opened thread %s (%s)", thread.Title, thread.ID), nil
	case "close":
		threadID := a.currentThreadID(src)
		if len(args) > 1 {
			threadID = strings.TrimSpace(args[1])
		}
		if threadID == "" {
			return "no active thread", nil
		}
		thread, err := a.closeThread(ctx, src, threadID)
		if err != nil {
			if err.Error() == "thread not found" {
				return "thread not found", nil
			}
			return "", err
		}
		return fmt.Sprintf("closed thread %s (%s)", thread.Title, thread.ID), nil
	default:
		return "usage: !thread [list|new <title>|open <thread_id>|close [thread_id]]", nil
	}
}
