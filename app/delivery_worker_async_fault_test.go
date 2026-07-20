package app

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abcdlsj/sumi/config"
	"github.com/abcdlsj/sumi/delivery"
	"github.com/abcdlsj/sumi/persona"
	"github.com/abcdlsj/sumi/space"
	"github.com/abcdlsj/sumi/task"
)

type asyncFaultEnv struct {
	t       *testing.T
	app     *App
	worker  *deliveryWorker
	channel string
	trigger string
	exec    func(ctx context.Context, req AsyncTurnRequest) AsyncTurnResult
	runs    int
}

func newAsyncFaultEnv(t *testing.T) *asyncFaultEnv {
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

	if _, err := a.Personas().Create("bob", persona.Meta{Runtime: "stub"}, "# Bob"); err != nil {
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

	env := &asyncFaultEnv{t: t, app: a, channel: ch.ID, trigger: trig.ID}
	env.exec = func(ctx context.Context, req AsyncTurnRequest) AsyncTurnResult {
		env.runs++
		return AsyncTurnResult{
			Content: "reply to " + req.Task.ExecutionIntent.Input,
			Steps:   []task.KeyStep{{Kind: task.KindSummary, Title: "done", At: time.Now(), OK: true}},
		}
	}
	a.RegisterAsyncTurnExecutor(func(ctx context.Context, req AsyncTurnRequest) AsyncTurnResult {
		return env.exec(ctx, req)
	})

	env.worker = newDeliveryWorker(a, "worker-test")
	env.worker.ctx = context.Background()
	return env
}

func (e *asyncFaultEnv) seedAsyncTask(input string) (*task.Task, *delivery.Delivery) {
	e.t.Helper()
	tk, err := e.app.Tasks().Create(task.CreateTaskInput{
		SpaceID:          e.channel,
		TriggerMessageID: e.trigger,
		InitiatorID:      "user",
		WorkerID:         "bob",
		Title:            "subtask",
		Source:           "desktop:channel:work",
		ExecutionIntent:  &task.ExecutionIntent{Input: input, Runtime: "stub", ShareContext: true},
	})
	if err != nil {
		e.t.Fatal(err)
	}
	d, err := e.app.EnqueueAsyncDelegate(tk)
	if err != nil {
		e.t.Fatal(err)
	}
	if d == nil {
		e.t.Fatal("EnqueueAsyncDelegate returned nil delivery")
	}
	return tk, d
}

func (e *asyncFaultEnv) claim(id string) (*delivery.Delivery, delivery.Fence) {
	e.t.Helper()
	d, err := e.app.Deliveries().Get(id)
	if err != nil {
		e.t.Fatal(err)
	}
	claimed, fence, err := e.app.Deliveries().ClaimNextInLane(d.SpaceID, d.ParentMessageID, d.AgentID, e.worker.ownerID, time.Now(), deliveryLeaseTTL)
	if err != nil {
		e.t.Fatalf("claim: %v", err)
	}
	return claimed, fence
}

func (e *asyncFaultEnv) placeholders(deliveryID string) []space.Message {
	e.t.Helper()
	sp, err := e.app.Spaces().LoadSpace(e.channel)
	if err != nil {
		e.t.Fatal(err)
	}
	var out []space.Message
	for _, m := range sp.Messages {
		if m.DeliveryID == deliveryID {
			out = append(out, m)
		}
	}
	return out
}

func TestFaultAsyncReplyBeforeComplete(t *testing.T) {
	e := newAsyncFaultEnv(t)
	tk, d := e.seedAsyncTask("do the thing")

	claimed, fence := e.claim(d.ID)
	e.worker.run(context.Background(), claimed, fence)

	got, err := e.app.Deliveries().Get(claimed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != delivery.StatusCompleted {
		t.Fatalf("delivery status = %q, want completed", got.Status)
	}
	ph := e.placeholders(claimed.ID)
	if len(ph) != 1 {
		t.Fatalf("placeholders = %d, want exactly 1", len(ph))
	}
	if got.ResultMessageID != ph[0].ID {
		t.Fatalf("delivery result %q != placeholder %q", got.ResultMessageID, ph[0].ID)
	}
	if strings.TrimSpace(ph[0].Content) == "" {
		t.Fatalf("finalized placeholder content empty, want reply text")
	}
	if ph[0].Status == "pending" {
		t.Fatalf("finalized placeholder still pending")
	}

	gotTask, err := e.app.Tasks().Get(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotTask.Status != task.StatusFinished {
		t.Fatalf("task status = %q, want finished", gotTask.Status)
	}
	if gotTask.ResultMessageID != ph[0].ID {
		t.Fatalf("task result %q != placeholder %q", gotTask.ResultMessageID, ph[0].ID)
	}
	if runs := len(mustRuns(t, e.app, tk.ID)); runs != 1 {
		t.Fatalf("task runs = %d, want exactly 1", runs)
	}
}

func TestFaultAsyncDuplicateReplay(t *testing.T) {
	e := newAsyncFaultEnv(t)
	tk, d := e.seedAsyncTask("do the thing")

	claimed, fence := e.claim(d.ID)
	e.worker.run(context.Background(), claimed, fence)
	firstRuns := e.runs
	firstPH := e.placeholders(claimed.ID)
	if len(firstPH) != 1 {
		t.Fatalf("after first run placeholders = %d, want 1", len(firstPH))
	}

	e.worker.run(context.Background(), claimed, fence)

	ph := e.placeholders(claimed.ID)
	if len(ph) != 1 || ph[0].ID != firstPH[0].ID {
		t.Fatalf("replay changed placeholder set: %+v (want stable single %q)", ph, firstPH[0].ID)
	}
	if e.runs != firstRuns {
		t.Fatalf("replay re-ran the turn (runs %d -> %d) on a completed delivery", firstRuns, e.runs)
	}
	if got := len(mustRuns(t, e.app, tk.ID)); got != 1 {
		t.Fatalf("task runs after replay = %d, want exactly 1", got)
	}
}

func TestFaultAsyncHeadlessFailureVisible(t *testing.T) {
	e := newAsyncFaultEnv(t)
	e.exec = func(ctx context.Context, req AsyncTurnRequest) AsyncTurnResult {
		e.runs++
		return AsyncTurnResult{Err: errors.New("model exploded")}
	}
	tk, d := e.seedAsyncTask("do the thing")

	claimed, fence := e.claim(d.ID)
	e.worker.run(context.Background(), claimed, fence)

	got, err := e.app.Deliveries().Get(claimed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != delivery.StatusFailed {
		t.Fatalf("delivery status = %q, want failed", got.Status)
	}
	ph := e.placeholders(claimed.ID)
	if len(ph) != 1 {
		t.Fatalf("placeholders = %d, want exactly 1 (visible failure)", len(ph))
	}
	if ph[0].Status == "pending" {
		t.Fatalf("failed placeholder still pending, want a terminal failed projection")
	}
	gotTask, err := e.app.Tasks().Get(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotTask.Status != task.StatusFailed {
		t.Fatalf("task status = %q, want failed", gotTask.Status)
	}
}

func TestFaultAsyncFailedRetrySamePlaceholder(t *testing.T) {
	e := newAsyncFaultEnv(t)
	e.exec = func(ctx context.Context, req AsyncTurnRequest) AsyncTurnResult {
		e.runs++
		return AsyncTurnResult{Err: errors.New("transient failure")}
	}
	tk, d := e.seedAsyncTask("do the thing")

	claimed, fence := e.claim(d.ID)
	e.worker.run(context.Background(), claimed, fence)
	failedPH := e.placeholders(claimed.ID)
	if len(failedPH) != 1 {
		t.Fatalf("after failure placeholders = %d, want 1", len(failedPH))
	}

	if _, err := e.app.Deliveries().Requeue(claimed.ID, time.Now()); err != nil {
		t.Fatalf("requeue: %v", err)
	}
	e.exec = func(ctx context.Context, req AsyncTurnRequest) AsyncTurnResult {
		e.runs++
		return AsyncTurnResult{Content: "recovered reply", Steps: []task.KeyStep{{Kind: task.KindSummary, Title: "done", At: time.Now(), OK: true}}}
	}

	claimed2, fence2 := e.claim(claimed.ID)
	e.worker.run(context.Background(), claimed2, fence2)

	got, err := e.app.Deliveries().Get(claimed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != delivery.StatusCompleted {
		t.Fatalf("delivery status after retry = %q, want completed", got.Status)
	}
	ph := e.placeholders(claimed.ID)
	if len(ph) != 1 {
		t.Fatalf("after retry placeholders = %d, want exactly 1 (same placeholder reused)", len(ph))
	}
	if ph[0].ID != failedPH[0].ID {
		t.Fatalf("retry bound a different message %q != %q", ph[0].ID, failedPH[0].ID)
	}
	if strings.TrimSpace(ph[0].Content) != "recovered reply" {
		t.Fatalf("recovered placeholder content = %q, want %q", ph[0].Content, "recovered reply")
	}
	gotTask, err := e.app.Tasks().Get(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotTask.Status != task.StatusFinished {
		t.Fatalf("task status after retry = %q, want finished", gotTask.Status)
	}
}

func TestFaultAsyncTaskOnlyCrashRecovery(t *testing.T) {
	e := newAsyncFaultEnv(t)
	tk, err := e.app.Tasks().Create(task.CreateTaskInput{
		SpaceID:          e.channel,
		TriggerMessageID: e.trigger,
		InitiatorID:      "user",
		WorkerID:         "bob",
		Title:            "subtask",
		Source:           "desktop:channel:work",
		ExecutionIntent:  &task.ExecutionIntent{Input: "do the thing", Runtime: "stub", ShareContext: true},
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := e.app.reconcileDeliveries(context.Background()); err != nil {
		t.Fatal(err)
	}
	ds, err := e.app.Deliveries().ListBySpace(e.channel)
	if err != nil {
		t.Fatal(err)
	}
	var async *delivery.Delivery
	for _, d := range ds {
		if d.Kind == delivery.KindAsyncDelegate && d.TaskID == tk.ID {
			async = d
		}
	}
	if async == nil {
		t.Fatalf("reconcile did not rebuild an async delivery from the Task fact: %+v", ds)
	}
	if async.OriginMessageID != tk.ID || async.ParentMessageID != e.trigger {
		t.Fatalf("rebuilt delivery identity = {origin:%q parent:%q}, want {%q %q}", async.OriginMessageID, async.ParentMessageID, tk.ID, e.trigger)
	}

	claimed, fence := e.claim(async.ID)
	e.worker.run(context.Background(), claimed, fence)

	got, err := e.app.Deliveries().Get(async.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != delivery.StatusCompleted {
		t.Fatalf("recovered delivery status = %q, want completed", got.Status)
	}
	gotTask, err := e.app.Tasks().Get(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotTask.Status != task.StatusFinished {
		t.Fatalf("recovered task status = %q, want finished", gotTask.Status)
	}
}

func mustRuns(t *testing.T, a *App, taskID string) []*task.Run {
	t.Helper()
	runs, err := a.Tasks().ListRuns(taskID)
	if err != nil {
		t.Fatal(err)
	}
	return runs
}
