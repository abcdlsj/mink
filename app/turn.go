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

func newStreamID() string {
	return "stream-" + uuid.NewString()[:8]
}

type turnFlow struct {
	app         *App
	runtime     agent.Runtime
	source      string
	personaID   string
	input       string
	attachments []msg.Attachment
	session     *session.Session
}

func (f turnFlow) run(ctx context.Context) error {
	baseline := 0
	if f.session != nil {
		baseline = len(f.session.Messages)
	}
	streamID := newStreamID()
	turn := &agent.Turn{
		Source:      f.source,
		Input:       f.input,
		Attachments: f.attachments,
		Session:     f.session,
		Bus:         f.app.bus,
		AgentID:     f.personaID,
		StreamID:    streamID,
	}
	if target := space.MapSource(f.source); target.Kind == space.KindAgentDM {
		if sp, _ := f.app.spaces.EnsureSpace(target.Kind, target.Seed, space.PersonaInfo{ID: target.Seed}); sp != nil {
			turn.SpaceID = sp.ID
		}
	}
	f.publishStream(bus.TurnStarted, "", turn)
	runErr := f.runtime.Run(ctx, turn)
	saveErr := f.app.sessions.Save(f.session)
	var spaceWriteErr error
	if saveErr == nil {
		spaceWriteErr = f.app.persistAssistantTurn(f.source, f.personaID, f.session, baseline)
	}
	if runErr != nil {
		err := turnErr(runErr, saveErr)
		f.publishStream(bus.TurnError, err.Error(), turn)
		if saveErr == nil {
			f.publishStream(bus.SessionUpdated, "", turn)
		}
		return err
	}
	if saveErr != nil {
		return saveErr
	}
	if spaceWriteErr != nil {
		f.publishStream(bus.TurnError, spaceWriteErr.Error(), turn)
		f.publishStream(bus.SessionUpdated, "", turn)
		return spaceWriteErr
	}
	f.publishStream(bus.SessionUpdated, "", turn)
	f.publishStream(bus.TurnFinished, "", turn)
	return nil
}

func (f turnFlow) publishStream(typ, errMsg string, turn *agent.Turn) {
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
	if s == nil {
		return nil
	}
	if space.MapSource(source).Kind != space.KindAgentDM {
		return nil
	}
	added := s.Messages[baseline:]
	content, reasoning := assembleAssistantOutput(added)
	if strings.TrimSpace(content) == "" && strings.TrimSpace(reasoning) == "" {
		return nil
	}
	_, err := a.appendAgentDMAssistantToSpace(source, personaID, content, reasoning, nil, "")
	return err
}

func (f turnFlow) publish(typ, err string) {
	f.app.bus.Publish(bus.Event{
		Type:      typ,
		Source:    f.source,
		SessionID: f.session.ID,
		Err:       err,
	})
}
