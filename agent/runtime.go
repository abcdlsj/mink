package agent

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/abcdlsj/mink/memory"
	rtsqlite "github.com/abcdlsj/mink/runtime/store/sqlite"
)

type runtimeTurn struct {
	TaskID string
	RunID  string
	Source string
}

type runtimeTurnKey struct{}

type teamTurnKey struct{}

func withRuntimeTurn(ctx context.Context, state rtsqlite.RunState, source string) context.Context {
	if state.TaskID == "" || state.RunID == "" {
		return ctx
	}
	return context.WithValue(ctx, runtimeTurnKey{}, runtimeTurn{
		TaskID: state.TaskID,
		RunID:  state.RunID,
		Source: source,
	})
}

func runtimeTurnFrom(ctx context.Context) (runtimeTurn, bool) {
	v, ok := ctx.Value(runtimeTurnKey{}).(runtimeTurn)
	return v, ok
}

func withTeamTurn(ctx context.Context, turn TeamTurn) context.Context {
	return context.WithValue(ctx, teamTurnKey{}, turn)
}

func teamTurnFrom(ctx context.Context) (TeamTurn, bool) {
	v, ok := ctx.Value(teamTurnKey{}).(TeamTurn)
	return v, ok
}

func speakerAgentID(ctx context.Context, fallback string) string {
	if turn, ok := teamTurnFrom(ctx); ok && strings.TrimSpace(turn.SpeakerAgentID) != "" {
		return turn.SpeakerAgentID
	}
	return fallback
}

func teamTurnPrompt(ctx context.Context) string {
	if turn, ok := teamTurnFrom(ctx); ok {
		return strings.TrimSpace(turn.Prompt)
	}
	return ""
}

func (a *Agent) appendEvent(ctx context.Context, typ, actorType string, payload any) {
	if a.rt == nil {
		return
	}
	turn, ok := runtimeTurnFrom(ctx)
	if !ok {
		return
	}
	if _, err := json.Marshal(payload); err != nil {
		return
	}
	_ = a.rt.AppendEvent(ctx, rtsqlite.Event{
		TaskID:    turn.TaskID,
		RunID:     turn.RunID,
		Type:      typ,
		ActorType: actorType,
		ActorID:   speakerAgentID(ctx, a.id),
		Source:    turn.Source,
		Payload:   payload,
	})
}

func (a *Agent) rememberSummary(ctx context.Context, source, summary, note string) {
	if a.mem == nil || strings.TrimSpace(summary) == "" {
		return
	}
	turn, ok := runtimeTurnFrom(ctx)
	if !ok {
		turn.Source = source
	}
	scopeSource := strings.TrimSpace(turn.Source)
	if scopeSource == "" {
		scopeSource = strings.TrimSpace(source)
	}
	_, _ = a.mem.PutScoped(ctx, memory.ChannelScope(scopeSource), memory.Doc{
		Title:   "Session summary",
		Kind:    "summary",
		TaskID:  turn.TaskID,
		RunID:   turn.RunID,
		Source:  scopeSource,
		Summary: strings.TrimSpace(note),
		Body:    summary,
	})
}
