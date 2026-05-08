package agent

import (
	"github.com/abcdlsj/sumi/bus"
)

type turnSink struct {
	turn *Turn
}

func Publish(t *Turn, ev bus.Event) {
	turnSink{turn: t}.Publish(ev)
}

func (s turnSink) Publish(ev bus.Event) {
	if s.turn == nil || s.turn.Bus == nil {
		return
	}
	if ev.Source == "" {
		ev.Source = s.turn.Source
	}
	if ev.SessionID == "" && s.turn.Session != nil {
		ev.SessionID = s.turn.Session.ID
	}
	s.turn.Bus.Publish(ev)
}
