package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/abcdlsj/mink/internal/xstr"
	"github.com/abcdlsj/mink/platform/cliapp"
	rtsqlite "github.com/abcdlsj/mink/runtime/store/sqlite"
)

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

func (a *App) teamStatusForSource(ctx context.Context, src string) *cliapp.TeamStatus {
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

	out := &cliapp.TeamStatus{
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
		out.Members = append(out.Members, cliapp.TeamMemberInfo{
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

func (a *App) threadInfoFromRecord(ctx context.Context, thread rtsqlite.TeamThread) cliapp.ThreadInfo {
	info := cliapp.ThreadInfo{
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

var compactLine = xstr.CompactLine
