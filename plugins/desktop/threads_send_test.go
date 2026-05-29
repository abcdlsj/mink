package desktop

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/abcdlsj/sumi/agent"
	"github.com/abcdlsj/sumi/app"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/persona"
	"github.com/abcdlsj/sumi/space"
)

type threadStubRuntime func(context.Context, *agent.Turn) error

func (f threadStubRuntime) Run(ctx context.Context, turn *agent.Turn) error { return f(ctx, turn) }

func registerThreadStubRuntime(a *app.App, fn func(context.Context, *agent.Turn) error) {
	a.RegisterRuntime("stub", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return threadStubRuntime(fn), nil
	})
}

func TestSendMessageThreadParentRejectedForAgentDM(t *testing.T) {
	b, a := newThreadBackend(t)
	dm, err := a.Spaces().EnsureSpace(space.KindAgentDM, "coder", space.PersonaInfo{ID: "coder", Display: "Coder"})
	if err != nil {
		t.Fatal(err)
	}
	root, err := a.Spaces().AppendUserMessage(dm.ID, "hi", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = b.SendMessage(SendRequest{SessionID: dm.ID, Input: "reply", ParentMessageID: root.ID})
	if err == nil {
		t.Fatal("expected error: thread reply not allowed in AgentDM")
	}
	if !strings.Contains(err.Error(), "thread") && !strings.Contains(err.Error(), "supported") {
		t.Fatalf("err = %q, want a thread-unsupported message", err.Error())
	}
}

func TestSendMessageThreadParentNotFoundReturnsError(t *testing.T) {
	b, a := newThreadBackend(t)
	sp := mustChannel(t, a)
	_, err := b.SendMessage(SendRequest{SessionID: sp.ID, Input: "reply", ParentMessageID: "msg-does-not-exist"})
	if err == nil {
		t.Fatal("expected error for unknown parent message")
	}
}

func TestSendMessageThreadReplyWritesReplyWithRootAsParent(t *testing.T) {
	b, a := newThreadBackend(t)
	registerThreadStubRuntime(a, func(ctx context.Context, turn *agent.Turn) error { return nil })
	sp := mustChannel(t, a)
	root, err := a.Spaces().AppendUserMessage(sp.ID, "kick", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.SendMessage(SendRequest{SessionID: sp.ID, Input: "reply text", ParentMessageID: root.ID}); err != nil {
		t.Fatal(err)
	}
	loaded, err := a.Spaces().LoadSpace(sp.ID)
	if err != nil {
		t.Fatal(err)
	}
	var reply *space.Message
	for i := range loaded.Messages {
		m := loaded.Messages[i]
		if m.AuthorKind == space.ParticipantUser && m.Content == "reply text" {
			reply = &m
			break
		}
	}
	if reply == nil {
		t.Fatal("reply message not found in Space")
	}
	if reply.ParentMessageID != root.ID {
		t.Fatalf("parent = %q, want %q (root)", reply.ParentMessageID, root.ID)
	}
}

func TestSendMessageThreadReplyToReplyNormalizesToRoot(t *testing.T) {
	b, a := newThreadBackend(t)
	registerThreadStubRuntime(a, func(ctx context.Context, turn *agent.Turn) error { return nil })
	sp := mustChannel(t, a)
	root, err := a.Spaces().AppendUserMessage(sp.ID, "kick", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.SendMessage(SendRequest{SessionID: sp.ID, Input: "first reply", ParentMessageID: root.ID}); err != nil {
		t.Fatal(err)
	}
	loaded, _ := a.Spaces().LoadSpace(sp.ID)
	var firstReplyID string
	for _, m := range loaded.Messages {
		if m.Content == "first reply" {
			firstReplyID = m.ID
			break
		}
	}
	if firstReplyID == "" {
		t.Fatal("first reply not stored")
	}
	if _, err := b.SendMessage(SendRequest{SessionID: sp.ID, Input: "reply of reply", ParentMessageID: firstReplyID}); err != nil {
		t.Fatal(err)
	}
	final, _ := a.Spaces().LoadSpace(sp.ID)
	var second *space.Message
	for i := range final.Messages {
		m := final.Messages[i]
		if m.Content == "reply of reply" {
			second = &m
			break
		}
	}
	if second == nil {
		t.Fatal("second reply not found")
	}
	if second.ParentMessageID != root.ID {
		t.Fatalf("parent = %q, want root id %q (no nesting)", second.ParentMessageID, root.ID)
	}
}

func TestSendMessageThreadAgentReplyInheritsRootParent(t *testing.T) {
	b, a := newThreadBackend(t)
	if _, err := a.Personas().Create("scout", persona.Meta{Display: "Scout", Runtime: "stub"}, ""); err != nil {
		t.Fatal(err)
	}
	registerThreadStubRuntime(a, func(ctx context.Context, turn *agent.Turn) error {
		turn.Session.Add(msg.Message{Role: "assistant", Content: "scout here"})
		return nil
	})
	sp := mustChannel(t, a)
	root, err := a.Spaces().AppendUserMessage(sp.ID, "kick", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.SendMessage(SendRequest{SessionID: sp.ID, Input: "@scout look here", ParentMessageID: root.ID}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		loaded, _ := a.Spaces().LoadSpace(sp.ID)
		for _, m := range loaded.Messages {
			if m.AuthorID == "scout" {
				if m.ParentMessageID != root.ID {
					t.Fatalf("scout reply parent = %q, want root %q", m.ParentMessageID, root.ID)
				}
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("scout never replied within thread")
}

func TestSendMessageThreadTwoSeparateAtMentionsBothFire(t *testing.T) {
	b, a := newThreadBackend(t)
	if _, err := a.Personas().Create("scout", persona.Meta{Display: "Scout", Runtime: "stub"}, ""); err != nil {
		t.Fatal(err)
	}
	registerThreadStubRuntime(a, func(ctx context.Context, turn *agent.Turn) error {
		turn.Session.Add(msg.Message{Role: "assistant", Content: "ack " + turn.Input})
		return nil
	})
	sp := mustChannel(t, a)
	root, err := a.Spaces().AppendUserMessage(sp.ID, "kick", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.SendMessage(SendRequest{SessionID: sp.ID, Input: "@scout first ask", ParentMessageID: root.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SendMessage(SendRequest{SessionID: sp.ID, Input: "@scout second ask", ParentMessageID: root.ID}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		loaded, _ := a.Spaces().LoadSpace(sp.ID)
		scoutReplies := 0
		for _, m := range loaded.Messages {
			if m.AuthorID == "scout" {
				scoutReplies++
				if m.ParentMessageID != root.ID {
					t.Fatalf("scout reply parent = %q, want root %q", m.ParentMessageID, root.ID)
				}
			}
		}
		if scoutReplies == 2 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	loaded, _ := a.Spaces().LoadSpace(sp.ID)
	count := 0
	for _, m := range loaded.Messages {
		if m.AuthorID == "scout" {
			count++
		}
	}
	t.Fatalf("scout replied %d times within thread, want 2 (each user @ should start its own routing chain, not be deduped)", count)
}
