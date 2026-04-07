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
		ActorID:   a.id,
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
	_, _ = a.mem.Put(ctx, "summaries", memory.Doc{
		Title:   "Session summary",
		Kind:    "summary",
		TaskID:  turn.TaskID,
		RunID:   turn.RunID,
		Source:  turn.Source,
		Summary: strings.TrimSpace(note),
		Body:    summary,
	})
}
