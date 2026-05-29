package collab

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/abcdlsj/sumi/agent"
	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/config"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/persona"
	"github.com/abcdlsj/sumi/space"
)

type metaCapture struct {
	mu     sync.Mutex
	events []bus.Event
}

func (c *metaCapture) record(events <-chan bus.Event, done chan<- struct{}) {
	for ev := range events {
		c.mu.Lock()
		c.events = append(c.events, ev)
		c.mu.Unlock()
	}
	close(done)
}

func (c *metaCapture) byType(typ string) []bus.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]bus.Event, 0)
	for _, ev := range c.events {
		if ev.Type == typ {
			out = append(out, ev)
		}
	}
	return out
}

func TestRunSpaceDelegatePublishesStreamMetadata(t *testing.T) {
	a := newTestApp(t, config.CollabConfig{PollTimeoutMS: 5000})
	if _, err := a.Personas().Create("coder", persona.Meta{Display: "Coder", Runtime: "stub"}, ""); err != nil {
		t.Fatal(err)
	}
	a.RegisterRuntime("stub", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			turn.Session.Add(msg.Message{Role: "assistant", Content: "audit done"})
			return nil
		}), nil
	})
	target := space.MapSource("desktop")
	sp, _ := a.Spaces().EnsureForSource("desktop", space.PersonaInfo{ID: target.Seed})
	if _, err := a.Spaces().AppendUserMessage(sp.ID, "kick", nil); err != nil {
		t.Fatal(err)
	}
	loaded, _ := a.Spaces().LoadSpace(sp.ID)
	triggerID := loaded.Messages[len(loaded.Messages)-1].ID

	events, cancel := a.Bus().Subscribe(128)
	cap := &metaCapture{}
	done := make(chan struct{})
	go cap.record(events, done)

	m := newManager(a)
	out, err := m.runWorkerSync(context.Background(), workerRunInput{
		Source:           "desktop",
		ParentSpaceID:    sp.ID,
		TriggerMessageID: triggerID,
		InitiatorID:      "user",
		WorkerID:         "coder",
		Runtime:          "stub",
		Title:            "audit",
		Input:            "audit",
	}, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = out
	cancel()
	<-done

	starts := cap.byType(bus.TurnStarted)
	if len(starts) == 0 {
		t.Fatal("expected runSpaceDelegate to publish turn.started")
	}
	first := starts[0]
	if first.AgentID != "coder" {
		t.Fatalf("AgentID = %q, want coder", first.AgentID)
	}
	if first.SpaceID != sp.ID {
		t.Fatalf("SpaceID = %q, want %q (parent space)", first.SpaceID, sp.ID)
	}
	if first.ParentMessageID != triggerID {
		t.Fatalf("ParentMessageID = %q, want %q (trigger message)", first.ParentMessageID, triggerID)
	}
	if first.StreamID == "" {
		t.Fatal("StreamID must be set")
	}

	chunks := cap.byType(bus.TurnChunk)
	for _, ev := range chunks {
		if ev.StreamID != first.StreamID {
			t.Fatalf("turn.chunk StreamID = %q, want %q (must match start)", ev.StreamID, first.StreamID)
		}
	}

	finishes := cap.byType(bus.TurnFinished)
	if len(finishes) == 0 {
		t.Fatal("expected turn.finished")
	}
	for _, ev := range finishes {
		if ev.StreamID == "" {
			t.Fatalf("turn.finished missing StreamID: %+v", ev)
		}
	}
}

func TestRunWorkerAsMentionPublishesStreamMetadata(t *testing.T) {
	a := newTestApp(t, config.CollabConfig{})
	if _, err := a.Personas().Create("reviewer", persona.Meta{Display: "Reviewer", Runtime: "stub"}, ""); err != nil {
		t.Fatal(err)
	}
	a.RegisterRuntime("stub", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			turn.Session.Add(msg.Message{Role: "assistant", Content: "looking at it"})
			return nil
		}), nil
	})
	target := space.MapSource("desktop")
	sp, _ := a.Spaces().EnsureForSource("desktop", space.PersonaInfo{ID: target.Seed})
	if _, err := a.Spaces().AppendUserMessage(sp.ID, "kick", nil); err != nil {
		t.Fatal(err)
	}
	loaded, _ := a.Spaces().LoadSpace(sp.ID)
	triggerID := loaded.Messages[len(loaded.Messages)-1].ID

	events, cancel := a.Bus().Subscribe(128)
	cap := &metaCapture{}
	done := make(chan struct{})
	go cap.record(events, done)

	m := newManager(a)
	if _, err := m.runWorkerAsMention(context.Background(), workerRunInput{
		Source:           "desktop",
		ParentSpaceID:    sp.ID,
		TriggerMessageID: triggerID,
		InitiatorID:      "user",
		WorkerID:         "reviewer",
		Runtime:          "stub",
		Input:            "have a look",
	}); err != nil {
		t.Fatal(err)
	}
	cancel()
	<-done

	starts := cap.byType(bus.TurnStarted)
	if len(starts) == 0 {
		t.Fatal("expected mention to publish turn.started")
	}
	first := starts[0]
	if first.AgentID != "reviewer" {
		t.Fatalf("AgentID = %q, want reviewer", first.AgentID)
	}
	if first.SpaceID != sp.ID {
		t.Fatalf("SpaceID = %q, want %q", first.SpaceID, sp.ID)
	}
	if first.ParentMessageID != triggerID {
		t.Fatalf("ParentMessageID = %q, want %q", first.ParentMessageID, triggerID)
	}
	if first.StreamID == "" {
		t.Fatal("StreamID must be set on mention turn events")
	}
}
