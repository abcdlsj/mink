package collab

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/abcdlsj/sumi/agent"
	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/persona"
	"github.com/abcdlsj/sumi/session"
	"github.com/abcdlsj/sumi/space"
	taskpkg "github.com/abcdlsj/sumi/task"
	"github.com/google/uuid"
)

var (
	ErrCollabAliasUnknown        = errors.New("collab: alias not bound in this source")
	ErrCollabAliasNotPersona     = errors.New("collab: alias resolves to a runtime, not a registered persona")
	ErrCollabPersonaNotFound     = errors.New("collab: persona not registered")
	ErrCollabAliasMissing        = errors.New("collab: alias is required to identify the worker")
	ErrCollabSpawnTimeoutCancel  = errors.New("collab: spawn timed out and was canceled")
	ErrCollabWorkerWroteNothing  = errors.New("collab: worker produced no visible reply")
)

func (m *manager) resolveCollabWorkerPersona(source, alias string) (*persona.Persona, error) {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return nil, ErrCollabAliasMissing
	}
	if p := m.app.Personas().Get(alias); p != nil {
		return p, nil
	}
	bound := m.aliasBinding(source, alias)
	if bound == "" {
		return nil, fmt.Errorf("%w: %s", ErrCollabAliasUnknown, alias)
	}
	if p := m.app.Personas().Get(bound); p != nil {
		return p, nil
	}
	if m.app.HasRuntime(bound) {
		return nil, fmt.Errorf("%w: alias=%s runtime=%s", ErrCollabAliasNotPersona, alias, bound)
	}
	return nil, fmt.Errorf("%w: %s", ErrCollabPersonaNotFound, bound)
}

func (m *manager) aliasBinding(source, alias string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.teams[source] == nil {
		return ""
	}
	return strings.TrimSpace(m.teams[source][alias])
}

type workerRunInput struct {
	Source           string
	ParentSpaceID    string
	TriggerMessageID string
	InitiatorID      string
	WorkerID         string
	Runtime          string
	Title            string
	Input            string
	ShareContext     bool
}

type workerRunOutcome struct {
	Status          taskpkg.Status
	ResultMessageID string
	Outcome         string
	Err             error
}

func (m *manager) runWorkerAsTask(ctx context.Context, in workerRunInput) (string, error) {
	worker := m.app.Personas().Get(in.WorkerID)
	if worker == nil {
		return "", fmt.Errorf("%w: %s", ErrCollabPersonaNotFound, in.WorkerID)
	}
	tk, err := m.app.Tasks().Create(taskpkg.CreateTaskInput{
		SpaceID:          in.ParentSpaceID,
		TriggerMessageID: in.TriggerMessageID,
		InitiatorID:      in.InitiatorID,
		WorkerID:         worker.ID,
		Title:            shortTitle(in.Title, in.Input),
		Source:           in.Source,
	})
	if err != nil {
		return "", err
	}
	go func() {
		_, _ = m.runSpaceDelegate(context.Background(), tk, spaceDelegateInput{
			ParentSpaceID:    in.ParentSpaceID,
			TriggerMessageID: in.TriggerMessageID,
			InitiatorID:      in.InitiatorID,
			WorkerID:         worker.ID,
			Title:            in.Title,
			Input:            in.Input,
			Runtime:          in.Runtime,
			Source:           in.Source,
			ShareContext:     in.ShareContext,
		}, worker)
	}()
	return tk.ID, nil
}

func (m *manager) runWorkerSync(ctx context.Context, in workerRunInput, timeout time.Duration) (workerRunOutcome, error) {
	worker := m.app.Personas().Get(in.WorkerID)
	if worker == nil {
		return workerRunOutcome{}, fmt.Errorf("%w: %s", ErrCollabPersonaNotFound, in.WorkerID)
	}
	tk, err := m.app.Tasks().Create(taskpkg.CreateTaskInput{
		SpaceID:          in.ParentSpaceID,
		TriggerMessageID: in.TriggerMessageID,
		InitiatorID:      in.InitiatorID,
		WorkerID:         worker.ID,
		Title:            shortTitle(in.Title, in.Input),
		Source:           in.Source,
	})
	if err != nil {
		return workerRunOutcome{}, err
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan *spaceDelegateOutcome, 1)
	go func() {
		out, _ := m.runSpaceDelegate(runCtx, tk, spaceDelegateInput{
			ParentSpaceID:    in.ParentSpaceID,
			TriggerMessageID: in.TriggerMessageID,
			InitiatorID:      in.InitiatorID,
			WorkerID:         worker.ID,
			Title:            in.Title,
			Input:            in.Input,
			Runtime:          in.Runtime,
			Source:           in.Source,
			ShareContext:     in.ShareContext,
		}, worker)
		done <- out
	}()
	select {
	case out := <-done:
		if out == nil {
			return workerRunOutcome{}, fmt.Errorf("collab: spawn produced no outcome (task=%s)", tk.ID)
		}
		final, _ := m.app.Tasks().Get(tk.ID)
		if final == nil {
			return workerRunOutcome{Status: out.Status, ResultMessageID: out.ResultMessageID, Err: out.Err}, out.Err
		}
		return workerRunOutcome{
			Status:          final.Status,
			ResultMessageID: final.ResultMessageID,
			Outcome:         final.Outcome,
			Err:             out.Err,
		}, out.Err
	case <-time.After(timeout):
		cancel()
		_ = m.app.Tasks().Cancel(tk.ID)
		<-done
		final, _ := m.app.Tasks().Get(tk.ID)
		outcome := workerRunOutcome{Status: taskpkg.StatusCanceled, Err: ErrCollabSpawnTimeoutCancel}
		if final != nil {
			outcome.Outcome = final.Outcome
		}
		return outcome, ErrCollabSpawnTimeoutCancel
	}
}

func (m *manager) runWorkerAsMention(ctx context.Context, in workerRunInput) (string, error) {
	worker := m.app.Personas().Get(in.WorkerID)
	if worker == nil {
		return "", fmt.Errorf("%w: %s", ErrCollabPersonaNotFound, in.WorkerID)
	}
	if strings.TrimSpace(in.ParentSpaceID) == "" {
		return "", fmt.Errorf("collab: mention requires a parent Space")
	}
	scratch := session.New("subtask:" + uuid.NewString()[:8])
	scratch.Add(msg.Message{Role: "user", Content: in.Input})
	baseline := len(scratch.Messages)
	rt, err := m.app.NewRuntimeFor(in.Runtime, worker)
	if err != nil {
		return "", err
	}
	streamID := "stream-" + uuid.NewString()[:8]
	turn := &agent.Turn{
		Source:          scratch.Source,
		Input:           in.Input,
		Session:         scratch,
		Bus:             m.app.Bus(),
		SpaceID:         in.ParentSpaceID,
		ParentMessageID: in.TriggerMessageID,
		AgentID:         worker.ID,
		StreamID:        streamID,
	}
	m.app.Bus().Publish(bus.Event{
		Type:            bus.TurnStarted,
		Source:          scratch.Source,
		SessionID:       scratch.ID,
		SpaceID:         turn.SpaceID,
		ParentMessageID: turn.ParentMessageID,
		AgentID:         turn.AgentID,
		StreamID:        turn.StreamID,
	})
	if err := rt.Run(ctx, turn); err != nil {
		m.app.Bus().Publish(bus.Event{
			Type:            bus.TurnError,
			Source:          scratch.Source,
			SessionID:       scratch.ID,
			Err:             err.Error(),
			SpaceID:         turn.SpaceID,
			ParentMessageID: turn.ParentMessageID,
			AgentID:         turn.AgentID,
			StreamID:        turn.StreamID,
		})
		return "", err
	}
	m.app.Bus().Publish(bus.Event{
		Type:            bus.TurnFinished,
		Source:          scratch.Source,
		SessionID:       scratch.ID,
		SpaceID:         turn.SpaceID,
		ParentMessageID: turn.ParentMessageID,
		AgentID:         turn.AgentID,
		StreamID:        turn.StreamID,
	})
	added := scratch.Messages[baseline:]
	content, reasoning := assembleAddedAssistantOutput(added)
	if strings.TrimSpace(content) == "" && strings.TrimSpace(reasoning) == "" {
		return "", ErrCollabWorkerWroteNothing
	}
	resolved := space.ParseMentions(content, nil, 0)
	written, err := m.app.Spaces().AppendAgentMessage(
		in.ParentSpaceID,
		space.PersonaInfo{ID: worker.ID, Display: worker.Display, Role: worker.Description},
		content,
		reasoning,
		resolved,
		in.TriggerMessageID,
	)
	if err != nil {
		return "", err
	}
	m.app.Bus().Publish(bus.Event{
		Type:   bus.ServiceNotice,
		Source: in.Source,
		Text:   fmt.Sprintf("mention completed: %s", worker.Display),
	})
	return written.ID, nil
}
