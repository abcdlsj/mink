package app

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/abcdlsj/sumi/agent"
	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/session"
	"github.com/abcdlsj/sumi/space"
)

func newStreamID() string { return "stream-" + uuid.NewString()[:8] }

type turnFlow struct {
	app                   *App
	runtime               agent.Runtime
	source                string
	personaID             string
	input                 string
	attachments           []msg.Attachment
	session               *session.Session
	includeHistory        bool
	disableExternalResume bool
}

func (f turnFlow) run(ctx context.Context) error {
	baseline := 0
	if f.session != nil {
		baseline = len(f.session.Messages)
	}
	turn := &agent.Turn{
		Source:                f.source,
		Input:                 f.input,
		Attachments:           f.attachments,
		Session:               f.session,
		Bus:                   f.app.bus,
		AgentID:               f.personaID,
		StreamID:              newStreamID(),
		IncludeHistory:        f.includeHistory,
		DisableExternalResume: f.disableExternalResume,
	}
	if space.MapSource(f.source).Kind == space.KindAgentDM {
		if sp, _, err := f.app.resolveAgentDMTargetSpace(f.source, f.personaID); err == nil && sp != nil {
			turn.SpaceID = sp.ID
		}
	}
	f.emit(bus.TurnStarted, "", turn)
	runErr := f.runtime.Run(ctx, turn)
	saveErr := f.app.sessions.Save(f.session)
	var writeErr error
	if saveErr == nil {
		writeErr = f.app.persistAssistantTurn(f.source, f.personaID, f.session, baseline)
	}
	if runErr != nil {
		err := turnErr(runErr, saveErr)
		f.emit(bus.TurnError, err.Error(), turn)
		if saveErr == nil {
			f.emit(bus.SessionUpdated, "", turn)
		}
		return err
	}
	if saveErr != nil {
		return saveErr
	}
	if writeErr != nil {
		f.emit(bus.TurnError, writeErr.Error(), turn)
		f.emit(bus.SessionUpdated, "", turn)
		return writeErr
	}
	f.emit(bus.SessionUpdated, "", turn)
	f.emit(bus.TurnFinished, "", turn)
	return nil
}

func (f turnFlow) emit(typ, errMsg string, turn *agent.Turn) {
	f.app.bus.Publish(bus.Event{
		Type:            typ,
		Source:          f.source,
		SessionID:       f.session.ID,
		Err:             errMsg,
		SpaceID:         turn.SpaceID,
		ParentMessageID: turn.ParentMessageID,
		AgentID:         turn.AgentID,
		StreamID:        turn.StreamID,
	})
}

func (a *App) persistAssistantTurn(source, personaID string, s *session.Session, baseline int) error {
	if s == nil || space.MapSource(source).Kind != space.KindAgentDM {
		return nil
	}
	added := s.Messages[baseline:]
	content, reasoning := msg.AssistantOutput(added)
	if strings.TrimSpace(content) == "" && strings.TrimSpace(reasoning) == "" {
		return nil
	}
	usage := msg.AssistantUsage(added)
	runtimeMeta := msg.AssistantRuntimeMeta(added)
	m, err := a.appendAgentDMAssistantToSpace(source, personaID, content, reasoning, nil, "", usage, runtimeMeta)
	if err != nil {
		return err
	}
	if m != nil && m.SpaceID != "" {
		go a.MaybeAutoTitleAgentDM(m.SpaceID)
	}
	return nil
}
