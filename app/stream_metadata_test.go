package app

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/abcdlsj/sumi/agent"
	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/command"
	"github.com/abcdlsj/sumi/config"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/persona"
	"github.com/abcdlsj/sumi/space"
)

func newStreamMetaApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	a, err := New(config.Config{
		Runtime:   "stub",
		DataDir:   filepath.Join(dir, "sumi-data"),
		Workspace: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

type streamCapture struct {
	mu     sync.Mutex
	events []bus.Event
}

func (c *streamCapture) capture(events <-chan bus.Event, done chan<- struct{}) {
	for ev := range events {
		c.mu.Lock()
		c.events = append(c.events, ev)
		c.mu.Unlock()
	}
	close(done)
}

func (c *streamCapture) byType(typ string) []bus.Event {
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

func TestAgentDMTurnPublishesStreamMetadata(t *testing.T) {
	a := newStreamMetaApp(t)
	if _, err := a.Personas().Create("tshoot", persona.Meta{Display: "Tshoot", Runtime: "stub"}, ""); err != nil {
		t.Fatal(err)
	}
	a.RegisterRuntime("stub", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			turn.Session.Add(msg.Message{Role: "assistant", Content: "ok"})
			return nil
		}), nil
	})

	events, cancel := a.Bus().Subscribe(64)
	cap := &streamCapture{}
	done := make(chan struct{})
	go cap.capture(events, done)

	if _, err := a.HandleInput(context.Background(), "desktop:agent:tshoot", "hi"); err != nil {
		t.Fatal(err)
	}
	cancel()
	<-done

	starts := cap.byType(bus.TurnStarted)
	if len(starts) == 0 {
		t.Fatal("expected at least one turn.started event")
	}
	first := starts[0]
	if first.AgentID != "tshoot" {
		t.Fatalf("AgentID = %q, want tshoot", first.AgentID)
	}
	if first.SpaceID == "" {
		t.Fatal("SpaceID must be set on AgentDM turn events")
	}
	if first.StreamID == "" {
		t.Fatal("StreamID must be set on every turn lifecycle event")
	}
	finishes := cap.byType(bus.TurnFinished)
	if len(finishes) == 0 {
		t.Fatal("expected at least one turn.finished event")
	}
	if finishes[0].StreamID != first.StreamID {
		t.Fatalf("turn.started StreamID = %q, turn.finished StreamID = %q, must match", first.StreamID, finishes[0].StreamID)
	}
}

func TestChannelWakePublishesSpaceAndParentAndAgent(t *testing.T) {
	a := newStreamMetaApp(t)
	if _, err := a.Personas().Create("coder", persona.Meta{Display: "Coder", Runtime: "stub"}, ""); err != nil {
		t.Fatal(err)
	}
	a.RegisterRuntime("stub", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			turn.Session.Add(msg.Message{Role: "assistant", Content: "looking at it"})
			return nil
		}), nil
	})
	target := space.MapSource("desktop")
	sp, err := a.Spaces().EnsureForSource("desktop", space.PersonaInfo{ID: target.Seed})
	if err != nil {
		t.Fatal(err)
	}

	events, cancel := a.Bus().Subscribe(128)
	cap := &streamCapture{}
	done := make(chan struct{})
	go cap.capture(events, done)

	if _, err := a.HandleInput(context.Background(), "desktop", "@coder kick off"); err != nil {
		t.Fatal(err)
	}
	cancel()
	<-done

	starts := cap.byType(bus.TurnStarted)
	if len(starts) == 0 {
		t.Fatal("expected wake to publish turn.started")
	}
	var found *bus.Event
	for i := range starts {
		if starts[i].AgentID == "coder" {
			found = &starts[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("no turn.started carries AgentID = coder, got %+v", starts)
	}
	if found.SpaceID != sp.ID {
		t.Fatalf("turn.started.SpaceID = %q, want %q (parent Space, not scratch)", found.SpaceID, sp.ID)
	}
	if found.StreamID == "" {
		t.Fatal("turn.started must carry StreamID")
	}
	chunks := cap.byType(bus.TurnChunk)
	for _, ev := range chunks {
		if ev.StreamID == "" {
			t.Fatalf("turn.chunk missing StreamID: %+v", ev)
		}
		if ev.AgentID == "" {
			t.Fatalf("turn.chunk missing AgentID: %+v", ev)
		}
		if ev.SpaceID != sp.ID {
			t.Fatalf("turn.chunk.SpaceID = %q, want %q", ev.SpaceID, sp.ID)
		}
	}
}

func TestThreadWakePublishesParentMessageID(t *testing.T) {
	a := newStreamMetaApp(t)
	if _, err := a.Personas().Create("scout", persona.Meta{Display: "Scout", Runtime: "stub"}, ""); err != nil {
		t.Fatal(err)
	}
	a.RegisterRuntime("stub", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			turn.Session.Add(msg.Message{Role: "assistant", Content: "scout here"})
			return nil
		}), nil
	})
	target := space.MapSource("desktop")
	sp, _ := a.Spaces().EnsureForSource("desktop", space.PersonaInfo{ID: target.Seed})
	root, err := a.Spaces().AppendUserMessage(sp.ID, "context", nil)
	if err != nil {
		t.Fatal(err)
	}

	events, cancel := a.Bus().Subscribe(128)
	cap := &streamCapture{}
	done := make(chan struct{})
	go cap.capture(events, done)

	ctx := context.Background()
	threadCtx := command.WithParentMessage(ctx, root.ID)
	if _, err := a.HandleInput(threadCtx, "desktop", "@scout in thread"); err != nil {
		t.Fatal(err)
	}
	cancel()
	<-done

	starts := cap.byType(bus.TurnStarted)
	var found *bus.Event
	for i := range starts {
		if starts[i].AgentID == "scout" {
			found = &starts[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("no turn.started for scout: %+v", starts)
	}
	if found.ParentMessageID != root.ID {
		t.Fatalf("ParentMessageID = %q, want %q (thread root)", found.ParentMessageID, root.ID)
	}
}
