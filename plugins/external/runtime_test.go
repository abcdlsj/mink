package external

import (
	"strings"
	"testing"

	"github.com/abcdlsj/mink/agent"
	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/msg"
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

func TestRuntimeBuildPromptUsesSharedSystemPrompt(t *testing.T) {
	r := &Runtime{
		driver: Driver{
			FormatHistory: func(messages []msg.Message) string {
				return "<conversation_history>\n[user]: old\n</conversation_history>"
			},
		},
		env: &agent.RuntimeEnv{
			Prompt: "项目约束",
		},
	}
	turn := &agent.Turn{
		Source:  "cli",
		Input:   "继续",
		Session: session.New("cli"),
	}
	turn.Session.Add(msg.Message{Role: "user", Content: "old"})

	out := r.buildPrompt(turn)
	for _, want := range []string{
		"<system_prompt>",
		"项目约束",
		"<conversation_history>",
		"<user_message>",
		"继续",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("prompt missing %q:\n%s", want, out)
		}
	}
}

func TestHandleMessageReturnsMsgErrorWithoutPublishingTurnError(t *testing.T) {
	b := bus.New()
	evs, cancel := b.Subscribe(8)
	defer cancel()

	turn := &agent.Turn{
		Source:  "test",
		Session: session.New("test"),
		Bus:     b,
	}
	st := &runState{}

	err := handleMessage("test", turn, st, &Message{Type: MsgError, Text: "boom"})
	if err == nil || err.Error() != "boom" {
		t.Fatalf("err = %v, want boom", err)
	}

	select {
	case ev := <-evs:
		t.Fatalf("unexpected event: %#v", ev)
	default:
	}
}
