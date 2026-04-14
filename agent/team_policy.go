package agent

import (
	"context"
	"fmt"
	"strings"

	rtsqlite "github.com/abcdlsj/mink/runtime/store/sqlite"
	"github.com/abcdlsj/mink/session"
)

func (d *TeamDispatcher) closeThread(ctx context.Context, threadID, summary string) {
	if d == nil || d.rt == nil || threadID == "" {
		return
	}
	thread, err := d.rt.GetThread(ctx, threadID)
	if err != nil || thread.ID == "" {
		return
	}
	_ = d.rt.UpdateThreadStatus(ctx, thread.ID, "closed")
	if d.sm != nil && thread.SessionID != "" {
		_ = d.sm.Update(thread.SessionID, func(s *session.Session) {
			s.SetKind("team_thread")
			s.SetStatus("closed")
			if strings.TrimSpace(s.Summary()) == "" && strings.TrimSpace(summary) != "" {
				s.SetSummary(summary)
			}
		})
		if sess, err := d.sm.Get(thread.SessionID); err == nil && sess != nil {
			_ = sess.Flush()
		}
	}
}

func (d *TeamDispatcher) AutoSchedule(ctx context.Context, src string, turn TeamTurn) (TeamHandoff, bool, error) {
	if d == nil || d.rt == nil || turn.TeamID == "" || turn.TurnPolicy != "round_robin" {
		return TeamHandoff{}, false, nil
	}
	members, err := d.rt.ListTeamMembers(ctx, turn.TeamID)
	if err != nil {
		return TeamHandoff{}, false, err
	}
	if len(members) < 2 {
		return TeamHandoff{}, false, nil
	}
	if turn.MaxRounds > 0 && turn.Round >= turn.MaxRounds {
		d.closeThread(ctx, turn.ThreadID, turn.Goal)
		return TeamHandoff{}, false, nil
	}
	if turn.SpeakerAgentID == turn.LeaderAgentID && turn.Round > 1 {
		return TeamHandoff{}, false, nil
	}
	next := members[turn.Round%len(members)]
	handoff := TeamHandoff{
		SpeakerAgentID: next.AgentID,
		Prompt:         d.roundRobinPrompt(turn, next),
	}
	d.mu.Lock()
	d.pending[src] = handoff
	d.mu.Unlock()
	return handoff, true, nil
}

func (d *TeamDispatcher) roundRobinPrompt(turn TeamTurn, member rtsqlite.TeamMember) string {
	role := strings.TrimSpace(member.RoleName)
	if role == "" {
		role = member.AgentID
	}
	if member.AgentID == turn.LeaderAgentID {
		return "Review the full team transcript, reconcile the specialist input, and provide the current best answer plus any remaining blockers."
	}
	if desc := strings.TrimSpace(member.RoleDescription); desc != "" {
		return fmt.Sprintf("Take the next visible team turn as %s. Focus on %s and move the thread toward a concrete answer.", role, desc)
	}
	return fmt.Sprintf("Take the next visible team turn as %s and add your concrete contribution to the thread.", role)
}
