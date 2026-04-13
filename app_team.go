package mink

import (
	"context"
	"fmt"
	"strings"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/platform"
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

func (a *App) currentTeamID(src string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.activeTeams[src]
}

func (a *App) currentThreadID(src string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.activeThreads[src]
}

func (a *App) setActiveTeam(src, teamID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if teamID == "" {
		delete(a.activeTeams, src)
		delete(a.activeThreads, src)
		return
	}
	a.activeTeams[src] = teamID
}

func (a *App) setActiveThread(src, threadID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if threadID == "" {
		delete(a.activeThreads, src)
		return
	}
	a.activeThreads[src] = threadID
}

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
		return "usage: !thread [list|new <title>|open <thread_id>]", nil
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
	default:
		return "usage: !thread [list|new <title>|open <thread_id>]", nil
	}
}

func (a *App) renderTeamList(ctx context.Context, src string) (string, error) {
	teams, err := a.rt.ListTeams(ctx, "")
	if err != nil {
		return "", err
	}
	if len(teams) == 0 {
		return "no teams", nil
	}
	current := a.currentTeamID(src)
	var b strings.Builder
	b.WriteString("Teams:\n")
	for _, team := range teams {
		mark := "  "
		if team.ID == current {
			mark = "* "
		}
		fmt.Fprintf(&b, "%s%s (%s) [%s|%s]\n", mark, team.Name, team.ID, team.Status, team.TurnPolicy)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

func (a *App) renderThreadList(ctx context.Context, src string) (string, error) {
	teamID := a.currentTeamID(src)
	if teamID == "" {
		return "no active team", nil
	}
	threads, err := a.rt.ListThreads(ctx, teamID, "")
	if err != nil {
		return "", err
	}
	if len(threads) == 0 {
		return "no threads", nil
	}
	current := a.currentThreadID(src)
	var b strings.Builder
	b.WriteString("Threads:\n")
	for _, thread := range threads {
		mark := "  "
		if thread.ID == current {
			mark = "* "
		}
		fmt.Fprintf(&b, "%s%s (%s) [%s]\n", mark, thread.Title, thread.ID, thread.Status)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

func (a *App) teamStatusForSource(ctx context.Context, src string) *platform.TeamStatus {
	if a.rt == nil {
		return nil
	}
	teamID := a.currentTeamID(src)
	if teamID == "" {
		return nil
	}

	team, err := a.rt.GetTeam(ctx, teamID)
	if err != nil || team.ID == "" {
		return nil
	}
	members, _ := a.rt.ListTeamMembers(ctx, teamID)
	threads, _ := a.rt.ListThreads(ctx, teamID, "")

	out := &platform.TeamStatus{
		ID:       team.ID,
		Name:     team.Name,
		Status:   team.Status,
		LeaderID: team.LeaderAgentID,
	}

	for _, member := range members {
		name := member.AgentID
		if ident, err := a.rt.GetAgentIdentity(ctx, member.AgentID); err == nil && ident.DisplayName != "" {
			name = ident.DisplayName
		}
		out.Members = append(out.Members, platform.TeamMemberInfo{
			ID:   member.AgentID,
			Name: name,
			Role: member.RoleName,
			Kind: member.MemberType,
		})
	}

	activeThreadID := a.currentThreadID(src)
	for i, thread := range threads {
		info := a.threadInfoFromRecord(ctx, thread)
		out.RecentThreads = append(out.RecentThreads, info)
		if i == 0 {
			out.LatestSummary = info.LatestSummary
			out.SummaryTime = info.LatestSummaryAt
		}
		if thread.ID == activeThreadID {
			threadCopy := info
			out.ActiveThread = &threadCopy
			if info.OpenBlockers != "" {
				out.CurrentBlocker = info.OpenBlockers
			}
		}
	}

	if out.LatestSummary == "" {
		out.LatestSummary = "No summary yet"
	}
	if out.CurrentBlocker == "" {
		out.CurrentBlocker = "No blocker tracked"
	}
	out.ActiveSpeaker = a.activeSpeaker()
	return out
}

func (a *App) threadInfoFromRecord(ctx context.Context, thread rtsqlite.TeamThread) platform.ThreadInfo {
	info := platform.ThreadInfo{
		ID:            thread.ID,
		Title:         thread.Title,
		Status:        thread.Status,
		UpdatedAt:     thread.UpdatedAt,
		CurrentRound:  thread.CurrentRound,
		Goal:          thread.Title,
		BestAnswer:    "No best answer yet",
		OpenBlockers:  "No blocker tracked",
		LatestSummary: "No summary yet",
	}
	if thread.SessionID == "" {
		return info
	}
	sess, err := a.sm.Get(thread.SessionID)
	if err != nil || sess == nil {
		return info
	}
	msgs := sess.Messages()
	if summary := strings.TrimSpace(sess.Summary()); summary != "" {
		info.LatestSummary = compactLine(summary, 120)
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if strings.TrimSpace(msgs[i].Content) == "" {
			continue
		}
		if msgs[i].Role == "assistant" {
			summary := compactLine(msgs[i].Content, 120)
			info.BestAnswer = summary
			info.LatestSummary = summary
			info.LatestSummaryAt = msgs[i].Timestamp.Format("15:04")
			break
		}
	}
	return info
}

func (a *App) activeSpeaker() string {
	if a.reg == nil {
		return ""
	}
	for _, state := range a.reg.All() {
		if string(state.Status) == "busy" {
			if state.Descriptor.Name != "" {
				return state.Descriptor.Name
			}
			return state.Descriptor.ID
		}
	}
	return ""
}

func compactLine(s string, limit int) string {
	s = strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
	if limit <= 0 || len([]rune(s)) <= limit {
		return s
	}
	runes := []rune(s)
	if limit < 2 {
		return string(runes[:limit])
	}
	return string(runes[:limit-1]) + "…"
}
