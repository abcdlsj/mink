package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/memory"
	"github.com/abcdlsj/mink/msg"
	rtsqlite "github.com/abcdlsj/mink/runtime/store/sqlite"
	"github.com/abcdlsj/mink/session"
)

type TeamBinding struct {
	TeamID   string
	ThreadID string
}

type TeamTurn struct {
	TeamID         string
	ThreadID       string
	LeaderAgentID  string
	SpeakerAgentID string
	SpeakerProfile string
	SpeakerRole    string
	Round          int
	MaxRounds      int
	Goal           string
	Prompt         string
	RuntimeSource  string
}

type TeamHandoff struct {
	SpeakerAgentID string
	Prompt         string
}

type TeamDispatcher struct {
	rt       *rtsqlite.DB
	mem      *memory.Store
	sm       *session.Manager
	mu       sync.RWMutex
	bindings map[string]TeamBinding
	locks    map[string]*sync.Mutex
	pending  map[string]TeamHandoff
}

func NewTeamDispatcher(rt *rtsqlite.DB, mem *memory.Store, sm *session.Manager) *TeamDispatcher {
	return &TeamDispatcher{
		rt:       rt,
		mem:      mem,
		sm:       sm,
		bindings: make(map[string]TeamBinding),
		locks:    make(map[string]*sync.Mutex),
		pending:  make(map[string]TeamHandoff),
	}
}

func (d *TeamDispatcher) BindSource(src, teamID, threadID string) {
	if strings.TrimSpace(src) == "" || strings.TrimSpace(teamID) == "" || strings.TrimSpace(threadID) == "" {
		return
	}
	d.mu.Lock()
	d.bindings[src] = TeamBinding{TeamID: teamID, ThreadID: threadID}
	d.mu.Unlock()
}

func (d *TeamDispatcher) UnbindSource(src string) {
	if strings.TrimSpace(src) == "" {
		return
	}
	d.mu.Lock()
	delete(d.bindings, src)
	d.mu.Unlock()
}

func (d *TeamDispatcher) Binding(src string) (TeamBinding, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	binding, ok := d.bindings[src]
	return binding, ok
}

func (d *TeamDispatcher) Schedule(src, speakerAgentID, prompt string) {
	if strings.TrimSpace(src) == "" || strings.TrimSpace(speakerAgentID) == "" {
		return
	}
	d.mu.Lock()
	d.pending[src] = TeamHandoff{
		SpeakerAgentID: speakerAgentID,
		Prompt:         strings.TrimSpace(prompt),
	}
	d.mu.Unlock()
}

func (d *TeamDispatcher) Pending(src string) (TeamHandoff, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	handoff, ok := d.pending[src]
	return handoff, ok
}

func (d *TeamDispatcher) takePending(src string) (TeamHandoff, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	handoff, ok := d.pending[src]
	if ok {
		delete(d.pending, src)
	}
	return handoff, ok
}

func (d *TeamDispatcher) lockFor(teamID string) func() {
	d.mu.Lock()
	lock, ok := d.locks[teamID]
	if !ok {
		lock = &sync.Mutex{}
		d.locks[teamID] = lock
	}
	d.mu.Unlock()
	lock.Lock()
	return lock.Unlock
}

func teamRuntimeSource(teamID, threadID string) string {
	return "team:" + teamID + ":" + threadID
}

func teamMemorySource(teamID string) string {
	return "team-memory:" + teamID
}

func (d *TeamDispatcher) Prepare(ctx context.Context, src string, sess *session.Session) (TeamTurn, func(), error) {
	if d == nil || d.rt == nil {
		return TeamTurn{}, nil, nil
	}
	binding, ok := d.Binding(src)
	if !ok {
		return TeamTurn{}, nil, nil
	}

	release := d.lockFor(binding.TeamID)
	thread, err := d.rt.GetThread(ctx, binding.ThreadID)
	if err != nil {
		release()
		return TeamTurn{}, nil, err
	}
	if thread.ID == "" {
		release()
		return TeamTurn{}, nil, fmt.Errorf("team thread %s not found", binding.ThreadID)
	}
	team, err := d.rt.GetTeam(ctx, binding.TeamID)
	if err != nil {
		release()
		return TeamTurn{}, nil, err
	}
	if team.ID == "" {
		release()
		return TeamTurn{}, nil, fmt.Errorf("team %s not found", binding.TeamID)
	}
	if sess != nil && thread.SessionID != "" && sess.ID() != thread.SessionID {
		if err := d.sm.RestoreSource(src, thread.SessionID); err != nil {
			release()
			return TeamTurn{}, nil, err
		}
	}
	if err := d.injectMemory(ctx, team, thread, sess); err != nil {
		release()
		return TeamTurn{}, nil, err
	}
	round, err := d.rt.IncrementThreadRound(ctx, thread.ID)
	if err != nil {
		release()
		return TeamTurn{}, nil, err
	}

	handoff, hasHandoff := d.takePending(src)
	speakerAgentID := firstNonEmpty(team.LeaderAgentID, bus.AddrAgentMain)
	prompt := ""
	if hasHandoff {
		speakerAgentID = handoff.SpeakerAgentID
		prompt = handoff.Prompt
	}
	identity, _ := d.rt.GetAgentIdentity(ctx, speakerAgentID)
	roleName := d.roleName(ctx, team.ID, speakerAgentID)

	return TeamTurn{
		TeamID:         team.ID,
		ThreadID:       thread.ID,
		LeaderAgentID:  firstNonEmpty(team.LeaderAgentID, bus.AddrAgentMain),
		SpeakerAgentID: speakerAgentID,
		SpeakerProfile: identity.Profile,
		SpeakerRole:    roleName,
		Round:          round,
		MaxRounds:      team.MaxRounds,
		Goal:           thread.Title,
		Prompt:         prompt,
		RuntimeSource:  teamRuntimeSource(team.ID, thread.ID),
	}, release, nil
}

func (d *TeamDispatcher) injectMemory(ctx context.Context, team rtsqlite.Team, thread rtsqlite.TeamThread, sess *session.Session) error {
	if d == nil || d.mem == nil || sess == nil || sess.EntryCount() > 0 {
		return nil
	}
	docs, err := d.mem.RecentBySource(ctx, teamMemorySource(team.ID), 3)
	if err != nil || len(docs) == 0 {
		return err
	}
	var b strings.Builder
	b.WriteString("[Team Memory]\n")
	b.WriteString("Recent summaries for team ")
	b.WriteString(team.Name)
	b.WriteString(" relevant to thread ")
	b.WriteString(thread.Title)
	b.WriteString(":\n")
	for _, doc := range docs {
		line := strings.TrimSpace(doc.Summary)
		if line == "" {
			line = strings.TrimSpace(doc.Body)
		}
		if line == "" {
			continue
		}
		b.WriteString("- ")
		b.WriteString(line)
		b.WriteByte('\n')
	}
	sess.Add(msg.Message{
		Role:    "system",
		Content: strings.TrimSpace(b.String()),
	})
	return nil
}

func (d *TeamDispatcher) Complete(ctx context.Context, turn TeamTurn, output string, runErr error) {
	if d == nil || d.mem == nil || runErr != nil {
		return
	}
	if turn.LeaderAgentID != "" && turn.SpeakerAgentID != turn.LeaderAgentID {
		return
	}
	output = strings.TrimSpace(output)
	if output == "" {
		return
	}
	_, _ = d.mem.Put(ctx, "team-memory", memory.Doc{
		Title:     "Thread summary",
		Kind:      "team_summary",
		Tags:      []string{turn.TeamID, turn.ThreadID},
		Source:    teamMemorySource(turn.TeamID),
		Summary:   compactSummary(output, 160),
		Body:      output,
		UpdatedAt: time.Now().UTC(),
	})
}

func compactSummary(s string, limit int) string {
	s = strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
	if limit <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	if limit == 1 {
		return string(runes[:1])
	}
	return string(runes[:limit-1]) + "…"
}

func firstNonEmpty(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

func (d *TeamDispatcher) roleName(ctx context.Context, teamID, agentID string) string {
	if d == nil || d.rt == nil || teamID == "" || agentID == "" {
		return ""
	}
	members, err := d.rt.ListTeamMembers(ctx, teamID)
	if err != nil {
		return ""
	}
	for _, member := range members {
		if member.AgentID == agentID {
			return member.RoleName
		}
	}
	return ""
}
