package session

import "github.com/abcdlsj/mink/msg"

func RestoreMessages(s *Session, msgs []msg.Message) {
	if s == nil || len(msgs) == 0 {
		return
	}
	for _, m := range msgs {
		s.Add(m)
	}
}
