package collab

import (
	"context"
	"strings"

	"github.com/abcdlsj/sumi/agent"
	"github.com/abcdlsj/sumi/app"
	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/session"
	"github.com/abcdlsj/sumi/space"
	"github.com/google/uuid"
)

func (m *manager) executeAsyncTurn(ctx context.Context, req app.AsyncTurnRequest) app.AsyncTurnResult {
	tk := req.Task
	worker := req.Worker
	if tk == nil || tk.ExecutionIntent == nil || worker == nil {
		return app.AsyncTurnResult{Err: ErrUnknownWorker}
	}
	intent := tk.ExecutionIntent

	scratch := session.New("subtask:" + uuid.NewString()[:8])
	if intent.ShareContext {
		if parent, err := m.app.CurrentSession(tk.Source); err == nil && parent != nil {
			for _, pm := range cloneMessages(parent.Messages) {
				scratch.Add(pm)
			}
		}
	}
	scratch.Add(msg.Message{Role: "user", Content: intent.Input})
	baseline := len(scratch.Messages)

	rt, err := m.app.NewRuntimeFor(intent.Runtime, worker)
	if err != nil {
		return app.AsyncTurnResult{Err: err}
	}

	streamID := strings.TrimSpace(req.StreamID)
	if streamID == "" {
		streamID = "stream-" + uuid.NewString()[:8]
	}
	turn := &agent.Turn{
		Source:          scratch.Source,
		Input:           intent.Input,
		Session:         scratch,
		Bus:             m.app.Bus(),
		SpaceID:         req.SpaceID,
		ParentMessageID: req.ParentMessageID,
		AgentID:         worker.ID,
		StreamID:        streamID,
	}
	stamp := func(ev bus.Event) bus.Event {
		ev.DeliveryID = req.DeliveryID
		if strings.TrimSpace(req.ResultMessageID) != "" {
			ev.MessageID = req.ResultMessageID
		}
		return ev
	}
	m.app.Bus().Publish(stamp(bus.Event{
		Type:            bus.TurnStarted,
		Source:          scratch.Source,
		SessionID:       scratch.ID,
		TaskID:          tk.ID,
		SpaceID:         turn.SpaceID,
		ParentMessageID: turn.ParentMessageID,
		AgentID:         turn.AgentID,
		StreamID:        turn.StreamID,
	}))
	runErr := rt.Run(ctx, turn)
	if runErr != nil {
		m.app.Bus().Publish(stamp(bus.Event{
			Type:            bus.TurnError,
			Source:          scratch.Source,
			SessionID:       scratch.ID,
			TaskID:          tk.ID,
			Err:             runErr.Error(),
			SpaceID:         turn.SpaceID,
			ParentMessageID: turn.ParentMessageID,
			AgentID:         turn.AgentID,
			StreamID:        turn.StreamID,
		}))
	} else {
		m.app.Bus().Publish(stamp(bus.Event{
			Type:            bus.TurnFinished,
			Source:          scratch.Source,
			SessionID:       scratch.ID,
			TaskID:          tk.ID,
			SpaceID:         turn.SpaceID,
			ParentMessageID: turn.ParentMessageID,
			AgentID:         turn.AgentID,
			StreamID:        turn.StreamID,
		}))
	}

	added := scratch.Messages[baseline:]
	steps := summarizeAddedSteps(added, runErr)
	if runErr != nil {
		return app.AsyncTurnResult{Steps: steps, Err: runErr}
	}
	content, reasoning := msg.AssistantOutput(added)
	if strings.TrimSpace(content) == "" && strings.TrimSpace(reasoning) == "" {
		return app.AsyncTurnResult{Steps: steps, EmptyOutput: true}
	}
	return app.AsyncTurnResult{
		Content:     content,
		Reasoning:   reasoning,
		Mentions:    space.ParseMentions(content, nil, 0),
		Usage:       msg.AssistantUsage(added),
		RuntimeMeta: msg.AssistantRuntimeMeta(added),
		Steps:       steps,
	}
}
