package app

import (
	"context"

	"github.com/abcdlsj/sumi/agent"
	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/session"
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
	runErr := f.runtime.Run(ctx, &agent.Turn{
		Source:      f.source,
		Input:       f.input,
		Attachments: f.attachments,
		Session:     f.session,
		Bus:         f.app.bus,
	})
	saveErr := f.app.sessions.Save(f.session)
	if saveErr == nil {
		// P1 dual-write: mirror the working agent's last reply into
		// the Space store. Skipped silently if personaID is empty
		// (Iris's hard rule — no surrogate authorship).
		f.app.dualWriteAssistantsFromSession(f.source, f.personaID, f.session)
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
	f.publish(bus.SessionUpdated, "")
	f.publish(bus.TurnFinished, "")
	return nil
}

func (f turnFlow) publish(typ, err string) {
	f.app.bus.Publish(bus.Event{
		Type:      typ,
		Source:    f.source,
		SessionID: f.session.ID,
		Err:       err,
	})
}
