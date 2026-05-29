package app

import (
	"context"
	"strings"

	"github.com/abcdlsj/sumi/agent"
	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/session"
	"github.com/abcdlsj/sumi/space"
)

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
	f.publish(bus.TurnStarted, "")
	baseline := 0
	if f.session != nil {
		baseline = len(f.session.Messages)
	}
	runErr := f.runtime.Run(ctx, &agent.Turn{
		Source:      f.source,
		Input:       f.input,
		Attachments: f.attachments,
		Session:     f.session,
		Bus:         f.app.bus,
	})
	saveErr := f.app.sessions.Save(f.session)
	var spaceWriteErr error
	if saveErr == nil {
		spaceWriteErr = f.app.persistAssistantTurn(f.source, f.personaID, f.session, baseline)
	}
	if runErr != nil {
		err := turnErr(runErr, saveErr)
		f.publish(bus.TurnError, err.Error())
		if saveErr == nil {
			f.publish(bus.SessionUpdated, "")
		}
		return err
	}
	if saveErr != nil {
		return saveErr
	}
	if spaceWriteErr != nil {
		f.publish(bus.TurnError, spaceWriteErr.Error())
		f.publish(bus.SessionUpdated, "")
		return spaceWriteErr
	}
	f.publish(bus.SessionUpdated, "")
	f.publish(bus.TurnFinished, "")
	return nil
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
