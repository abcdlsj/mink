package external

import (
	"strings"
	"testing"

	"github.com/abcdlsj/sumi/agent"
	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/session"
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
	handleMessage("test", turn, st, &Message{Type: MsgAssistantText, Text: "hello", Snapshot: true})
	st.flush(turn.Session)

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
			if got := turn.Session.Messages[len(turn.Session.Messages)-1].Content; got != "hello" {
				t.Fatalf("assistant = %q, want %q", got, "hello")
			}
			return
		}
	}
}

func TestHandleMessageMergesAssistantSnapshots(t *testing.T) {
	b := bus.New()
	evs, cancel := b.Subscribe(8)
	defer cancel()

	turn := &agent.Turn{
		Source:  "test",
		Session: session.New("test"),
		Bus:     b,
	}
	st := &runState{}

	handleMessage("test", turn, st, &Message{Type: MsgAssistantText, Text: "hel", Snapshot: true})
	handleMessage("test", turn, st, &Message{Type: MsgAssistantText, Text: "hello", Snapshot: true})
	handleMessage("test", turn, st, &Message{Type: MsgAssistantText, Text: "hello", Snapshot: true})
	st.flush(turn.Session)

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
			if got := turn.Session.Messages[len(turn.Session.Messages)-1].Content; got != "hello" {
				t.Fatalf("assistant = %q, want %q", got, "hello")
			}
			return
		}
	}
}

func TestHandleMessagePublishesThinking(t *testing.T) {
	b := bus.New()
	evs, cancel := b.Subscribe(8)
	defer cancel()

	turn := &agent.Turn{
		Source:  "test",
		Session: session.New("test"),
		Bus:     b,
	}
	st := &runState{}

	handleMessage("test", turn, st, &Message{Type: MsgThinkingChunk, Text: "think"})
	st.flush(turn.Session)

	select {
	case ev := <-evs:
		if ev.Type != bus.TurnReasoning || ev.Text != "think" {
			t.Fatalf("event = %#v", ev)
		}
	default:
		t.Fatal("missing thinking event")
	}
	if got := turn.Session.Messages[len(turn.Session.Messages)-1].Reasoning; got != "think" {
		t.Fatalf("reasoning = %q", got)
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
		"<user_message>",
		"继续",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("prompt missing %q:\n%s", want, out)
		}
	}
}

func TestRuntimeSessionIDResumeFlag(t *testing.T) {
	r := &Runtime{driver: Driver{Name: "claude"}}
	s := session.New("test")

	id, resume := r.getOrCreateSessionID(s)
	if id == "" || resume {
		t.Fatalf("first = %q %v", id, resume)
	}
	got, resume := r.getOrCreateSessionID(s)
	if got != id || !resume {
		t.Fatalf("second = %q %v, want %q true", got, resume, id)
	}
}

func TestRuntimeSessionIDIsScopedByWorkspace(t *testing.T) {
	s := session.New("test")
	sumi := &Runtime{driver: Driver{Name: "claude"}, workspace: "/tmp/sumi"}
	dyn := &Runtime{driver: Driver{Name: "claude"}, workspace: "/tmp/go-dynamic"}

	first, resume := sumi.getOrCreateSessionID(s)
	if first == "" || resume {
		t.Fatalf("first = %q %v", first, resume)
	}
	second, resume := dyn.getOrCreateSessionID(s)
	if second == "" || resume {
		t.Fatalf("second = %q %v", second, resume)
	}
	if first == second {
		t.Fatalf("workspace sessions share id %q", first)
	}
	if got := s.ExternalSession["claude:/tmp/sumi"]; got != first {
		t.Fatalf("sumi session = %q, want %q", got, first)
	}
	if got := s.ExternalSession["claude:/tmp/go-dynamic"]; got != second {
		t.Fatalf("dynamic session = %q, want %q", got, second)
	}
}

func TestRuntimeResetSessionIDReplacesStaleExternalSession(t *testing.T) {
	r := &Runtime{driver: Driver{Name: "claude"}}
	s := session.New("test")
	s.ExternalSession["claude"] = "stale"

	next := r.resetSessionID(s)
	if next == "" || next == "stale" {
		t.Fatalf("next = %q", next)
	}
	if got := s.ExternalSession["claude"]; got != next {
		t.Fatalf("external session = %q, want %q", got, next)
	}
}

func TestMissingExternalSessionDetectsClaudeResumeError(t *testing.T) {
	err := wrapMessageError("claude", &Message{Type: MsgError, Text: "error_during_execution: No conversation found with session ID: 67c492f4-8d18-4552-b6cc-698de0082a2d"})
	if !missingExternalSession(err) {
		t.Fatalf("missingExternalSession(%v) = false", err)
	}
	if missingExternalSession(wrapMessageError("claude", &Message{Type: MsgError, Text: "auth failed"})) {
		t.Fatal("auth failure detected as missing session")
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
