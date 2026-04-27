package app

import (
	"context"

	"github.com/abcdlsj/mink/agent"
	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/session"
)

type turnFlow struct {
	app     *App
	runtime agent.Runtime
	source  string
	input   string
	session *session.Session
}

func (f turnFlow) run(ctx context.Context) error {
	f.publish(bus.TurnStarted, "")
	runErr := f.runtime.Run(ctx, &agent.Turn{
		Source:  f.source,
		Input:   f.input,
		Session: f.session,
		Bus:     f.app.bus,
	})
	saveErr := f.app.sessions.Save(f.session)
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
