package collab

import (
	"context"
	"testing"
	"time"

	"github.com/abcdlsj/sumi/agent"
	"github.com/abcdlsj/sumi/app"
	"github.com/abcdlsj/sumi/config"
	"github.com/abcdlsj/sumi/delivery"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/persona"
	"github.com/abcdlsj/sumi/space"
)

type collabFaultEnv struct {
	t       *testing.T
	app     *app.App
	m       *manager
	channel *space.Space
	trigger space.Message
	worker  *persona.Persona
	source  string
}

func newCollabFaultEnv(t *testing.T) *collabFaultEnv {
	t.Helper()
	a := newTestApp(t, config.CollabConfig{})
	a.RegisterRuntime("stub", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			turn.Session.Add(msg.Message{Role: "user", Content: turn.Input})
			turn.Session.Add(msg.Message{Role: "assistant", Content: "reply to " + turn.Input})
			return nil
		}), nil
	})

	worker, err := a.Personas().Create("bob", persona.Meta{Runtime: "stub"}, "# Bob")
	if err != nil {
		t.Fatal(err)
	}

	ch, err := a.Spaces().EnsureSpace(space.KindChannel, "work", space.PersonaInfo{})
	if err != nil {
		t.Fatal(err)
	}
	trig, err := a.Spaces().AppendUserMessage(ch.ID, "please delegate this", nil)
	if err != nil {
		t.Fatal(err)
	}
	return &collabFaultEnv{
		t:       t,
		app:     a,
		m:       newManager(a),
		channel: ch,
		trigger: trig,
		worker:  worker,
		source:  "desktop:channel:work",
	}
}

func (e *collabFaultEnv) asyncInput() spaceDelegateInput {
	return spaceDelegateInput{
		ParentSpaceID:    e.channel.ID,
		TriggerMessageID: e.trigger.ID,
		InitiatorID:      "user",
		WorkerID:         e.worker.ID,
		Title:            "subtask title",
		Input:            "do the thing",
		Runtime:          "stub",
		Source:           e.source,
		ShareContext:     true,
	}
}

func (e *collabFaultEnv) asyncDeliveries() []*delivery.Delivery {
	e.t.Helper()
	all, err := e.app.Deliveries().ListBySpace(e.channel.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	var out []*delivery.Delivery
	for _, d := range all {
		if d.Kind == delivery.KindAsyncDelegate {
			out = append(out, d)
		}
	}
	return out
}

func TestDelegateAsyncPersistsTaskExecutionIntent(t *testing.T) {
	e := newCollabFaultEnv(t)
	in := e.asyncInput()

	taskID, err := e.m.delegateAsync(context.Background(), in)
	if err != nil {
		t.Fatalf("delegateAsync: %v", err)
	}
	tk, err := e.app.Tasks().Get(taskID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if tk.ExecutionIntent == nil {
		t.Fatalf("task has nil ExecutionIntent; async Delegate must persist the immutable execution contract before running")
	}
	if tk.ExecutionIntent.Input != in.Input {
		t.Fatalf("intent.Input = %q, want %q", tk.ExecutionIntent.Input, in.Input)
	}
	if tk.ExecutionIntent.Runtime != in.Runtime {
		t.Fatalf("intent.Runtime = %q, want %q", tk.ExecutionIntent.Runtime, in.Runtime)
	}
	if tk.ExecutionIntent.ShareContext != in.ShareContext {
		t.Fatalf("intent.ShareContext = %v, want %v", tk.ExecutionIntent.ShareContext, in.ShareContext)
	}
	if tk.SpaceID != in.ParentSpaceID || tk.TriggerMessageID != in.TriggerMessageID || tk.WorkerID != in.WorkerID {
		t.Fatalf("task facts = {space:%q trigger:%q worker:%q}, want {%q %q %q}",
			tk.SpaceID, tk.TriggerMessageID, tk.WorkerID, in.ParentSpaceID, in.TriggerMessageID, in.WorkerID)
	}
}

func TestDelegateAsyncCreatesAsyncDeliveryReferencingTask(t *testing.T) {
	e := newCollabFaultEnv(t)
	in := e.asyncInput()

	taskID, err := e.m.delegateAsync(context.Background(), in)
	if err != nil {
		t.Fatalf("delegateAsync: %v", err)
	}
	ds := e.asyncDeliveries()
	if len(ds) != 1 {
		t.Fatalf("async deliveries = %d, want exactly 1 (volatile goroutine must be replaced by a durable Delivery)", len(ds))
	}
	d := ds[0]
	if d.TaskID != taskID {
		t.Fatalf("delivery.TaskID = %q, want %q (Delivery must reference the Task)", d.TaskID, taskID)
	}
	if d.OriginMessageID != taskID {
		t.Fatalf("delivery.OriginMessageID = %q, want Task.ID %q (one Delivery per Task)", d.OriginMessageID, taskID)
	}
	if d.ParentMessageID != in.TriggerMessageID {
		t.Fatalf("delivery.ParentMessageID = %q, want trigger %q (reply stays threaded under trigger)", d.ParentMessageID, in.TriggerMessageID)
	}
	if d.SpaceID != in.ParentSpaceID || d.AgentID != in.WorkerID {
		t.Fatalf("delivery = {space:%q agent:%q}, want {%q %q}", d.SpaceID, d.AgentID, in.ParentSpaceID, in.WorkerID)
	}
	if d.Status != delivery.StatusPending && d.Status != delivery.StatusCompleted {
		t.Fatalf("fresh async delivery status = %q, want pending (or completed if the worker already drove it)", d.Status)
	}
}

func TestDelegateAsyncSameTaskCreateIfAbsent(t *testing.T) {
	e := newCollabFaultEnv(t)
	in := e.asyncInput()

	taskID, err := e.m.delegateAsync(context.Background(), in)
	if err != nil {
		t.Fatalf("delegateAsync: %v", err)
	}
	if _, _, err := e.app.Deliveries().CreateIfAbsent(&delivery.Delivery{
		Kind:            delivery.KindAsyncDelegate,
		SpaceID:         in.ParentSpaceID,
		ParentMessageID: in.TriggerMessageID,
		OriginMessageID: taskID,
		AgentID:         in.WorkerID,
		TaskID:          taskID,
	}, time.Now()); err != nil {
		t.Fatalf("replay CreateIfAbsent: %v", err)
	}
	if ds := e.asyncDeliveries(); len(ds) != 1 {
		t.Fatalf("same-Task replay = %d async deliveries, want exactly 1 (create-if-absent by Task.ID)", len(ds))
	}
}

func TestDelegateAsyncDistinctTasksSameTriggerNoCollision(t *testing.T) {
	e := newCollabFaultEnv(t)

	in1 := e.asyncInput()
	in1.Input = "first job"
	in2 := e.asyncInput()
	in2.Input = "second job"

	id1, err := e.m.delegateAsync(context.Background(), in1)
	if err != nil {
		t.Fatalf("delegateAsync #1: %v", err)
	}
	id2, err := e.m.delegateAsync(context.Background(), in2)
	if err != nil {
		t.Fatalf("delegateAsync #2: %v", err)
	}
	if id1 == id2 {
		t.Fatalf("two Delegates produced the same TaskID %q", id1)
	}
	ds := e.asyncDeliveries()
	if len(ds) != 2 {
		t.Fatalf("two distinct Delegates on same trigger = %d async deliveries, want 2 (no collision)", len(ds))
	}
	seen := map[string]bool{}
	for _, d := range ds {
		seen[d.TaskID] = true
	}
	if !seen[id1] || !seen[id2] {
		t.Fatalf("deliveries do not reference both Tasks: got TaskIDs %v, want {%q,%q}", seen, id1, id2)
	}
}

func TestSpawnSyncZeroAsyncDelivery(t *testing.T) {
	e := newCollabFaultEnv(t)
	in := e.asyncInput()

	if _, err := e.m.delegateInSpace(context.Background(), in); err != nil {
		t.Fatalf("delegateInSpace: %v", err)
	}
	if ds := e.asyncDeliveries(); len(ds) != 0 {
		t.Fatalf("spawn-sync created %d async deliveries, want 0 (synchronous shortest path)", len(ds))
	}
}

func TestMentionZeroAsyncDelivery(t *testing.T) {
	e := newCollabFaultEnv(t)

	if _, err := e.m.runWorkerAsMention(context.Background(), workerRunInput{
		Source:           e.source,
		ParentSpaceID:    e.channel.ID,
		TriggerMessageID: e.trigger.ID,
		InitiatorID:      "user",
		WorkerID:         e.worker.ID,
		Runtime:          "stub",
		Input:            "quick question",
	}); err != nil {
		t.Fatalf("runWorkerAsMention: %v", err)
	}
	if ds := e.asyncDeliveries(); len(ds) != 0 {
		t.Fatalf("mention created %d async deliveries, want 0 (synchronous inline path)", len(ds))
	}
}
