package external

import (
	"strings"
	"testing"

	"github.com/abcdlsj/mink/agent"
	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/session"
)

func TestHandleMessageDoesNotRepublishFinalAssistantTextAfterStreaming(t *testing.T) {
	b := bus.New()
	evs, cancel := b.Subscribe(8)
	defer cancel()

	turn := &agent.Turn{
		Source:  "test",
		Session: session.New("test"),
		Bus:     b,
	}
	st := &runState{}

	handleMessage("test", turn, st, &Message{Type: MsgStreamChunk, Text: "hello"})
	handleMessage("test", turn, st, &Message{Type: MsgAssistantText, Text: "hello"})

	var chunks []string
	for {
		select {
		case ev := <-evs:
			if ev.Type == bus.TurnChunk {
				chunks = append(chunks, ev.Text)
			}
		default:
			if got := strings.Join(chunks, ""); got != "hello" {
				t.Fatalf("chunks = %q, want %q", got, "hello")
			}
			return
		}
	}
}
