package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abcdlsj/sumi/bus"
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

func TestFaultAsyncReplyPersistedNotCompletedReclaim(t *testing.T) {
	e := newAsyncFaultEnv(t)
	tk, d := e.seedAsyncTask("do the thing")

	past := time.Now().Add(-2 * deliveryLeaseTTL)
	_, fenceA, err := e.app.Deliveries().ClaimNextInLane(d.SpaceID, d.ParentMessageID, d.AgentID, "owner-A", past, deliveryLeaseTTL)
	if err != nil {
		t.Fatalf("claim A: %v", err)
	}
	personas := e.app.fuzzyPersonaResolver()
	ph, _, err := e.app.Spaces().EnsureDeliveryPlaceholder(d.SpaceID, d.ID, d.AgentID, d.ParentMessageID, personas.Info)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.app.Deliveries().BindResultMessage(d.ID, fenceA, ph.ID, past); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if _, _, err := e.app.Spaces().FinalizeDeliveryMessage(d.SpaceID, ph.ID, d.ID, fenceA.OwnerID, fenceA.Version, past, func(m *space.Message) {
		m.Content = "reply written before crash"
		m.Status = ""
	}, nil, personas.Info, nil); err != nil {
		t.Fatalf("A finalize: %v", err)
	}

	beforeReclaim := e.placeholders(d.ID)
	if len(beforeReclaim) != 1 {
		t.Fatalf("placeholders before reclaim = %d, want 1", len(beforeReclaim))
	}
	preGot, err := e.app.Deliveries().Get(d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if preGot.Status == delivery.StatusCompleted {
		t.Fatalf("delivery already completed before reclaim; the interrupt window was not reproduced")
	}

	claimed2, fence2, err := e.app.Deliveries().ClaimNextInLane(d.SpaceID, d.ParentMessageID, d.AgentID, "owner-B", time.Now(), deliveryLeaseTTL)
	if err != nil {
		t.Fatalf("reclaim B: %v", err)
	}
	if !(fence2.Version > fenceA.Version) {
		t.Fatalf("reclaim fence version %d not greater than A %d", fence2.Version, fenceA.Version)
	}
	if claimed2.ResultMessageID != ph.ID {
		t.Fatalf("ResultMessageID = %q, want %q preserved across reclaim", claimed2.ResultMessageID, ph.ID)
	}
	e.worker.run(context.Background(), claimed2, fence2)

	got, err := e.app.Deliveries().Get(d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != delivery.StatusCompleted {
		t.Fatalf("delivery status after reclaim = %q, want completed", got.Status)
	}
	ph2 := e.placeholders(d.ID)
	if len(ph2) != 1 {
		t.Fatalf("placeholders after reclaim = %d, want exactly 1", len(ph2))
	}
	if ph2[0].ID != ph.ID {
		t.Fatalf("reclaim bound a different message %q != %q", ph2[0].ID, ph.ID)
	}
	if got.ResultMessageID != ph2[0].ID {
		t.Fatalf("delivery result %q != placeholder %q", got.ResultMessageID, ph2[0].ID)
	}
	if strings.TrimSpace(ph2[0].Content) == "" {
		t.Fatalf("finalized placeholder content empty, want reply text")
	}
	if ph2[0].Status == "pending" {
		t.Fatalf("finalized placeholder still pending after reclaim")
	}

	gotTask, err := e.app.Tasks().Get(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotTask.Status != task.StatusFinished {
		t.Fatalf("task status = %q, want finished", gotTask.Status)
	}
	if gotTask.ResultMessageID != ph2[0].ID {
		t.Fatalf("task result %q != placeholder %q", gotTask.ResultMessageID, ph2[0].ID)
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

func TestFaultAsyncCancelDuringRunReclaimable(t *testing.T) {
	e := newAsyncFaultEnv(t)
	tk, d := e.seedAsyncTask("do the thing")

	entered := make(chan struct{})
	e.exec = func(ctx context.Context, req AsyncTurnRequest) AsyncTurnResult {
		e.runs++
		close(entered)
		<-ctx.Done()
		return AsyncTurnResult{Err: ctx.Err()}
	}

	claimed, fence := e.claim(d.ID)
	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		e.worker.run(runCtx, claimed, fence)
		close(done)
	}()
	<-entered
	cancel()
	<-done

	got, err := e.app.Deliveries().Get(claimed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status == delivery.StatusFailed || got.Status == delivery.StatusCompleted {
		t.Fatalf("delivery status after cancel = %q, want a non-terminal reclaimable state", got.Status)
	}
	gotTask, err := e.app.Tasks().Get(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotTask.Status == task.StatusFailed {
		t.Fatalf("task marked failed on a normal cancel; want reclaimable")
	}
	ph := e.placeholders(claimed.ID)
	if len(ph) != 1 {
		t.Fatalf("placeholders after cancel = %d, want exactly 1", len(ph))
	}
	if ph[0].Status != "pending" {
		t.Fatalf("placeholder status after cancel = %q, want still pending", ph[0].Status)
	}

	e.exec = func(ctx context.Context, req AsyncTurnRequest) AsyncTurnResult {
		e.runs++
		return AsyncTurnResult{Content: "reclaimed reply", Steps: []task.KeyStep{{Kind: task.KindSummary, Title: "done", At: time.Now(), OK: true}}}
	}
	future := time.Now().Add(2 * deliveryLeaseTTL)
	claimed2, fence2, err := e.app.Deliveries().ClaimNextInLane(d.SpaceID, d.ParentMessageID, d.AgentID, "owner-B", future, deliveryLeaseTTL)
	if err != nil {
		t.Fatalf("reclaim B: %v", err)
	}
	if !(fence2.Version > fence.Version) {
		t.Fatalf("reclaim fence version %d not greater than %d", fence2.Version, fence.Version)
	}
	e.worker.run(context.Background(), claimed2, fence2)

	got, err = e.app.Deliveries().Get(claimed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != delivery.StatusCompleted {
		t.Fatalf("delivery status after reclaim = %q, want completed", got.Status)
	}
	ph2 := e.placeholders(claimed.ID)
	if len(ph2) != 1 {
		t.Fatalf("placeholders after reclaim = %d, want exactly 1", len(ph2))
	}
	if ph2[0].ID != ph[0].ID {
		t.Fatalf("reclaim bound a different message %q != %q", ph2[0].ID, ph[0].ID)
	}
	gotTask, err = e.app.Tasks().Get(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotTask.Status != task.StatusFinished {
		t.Fatalf("task status after reclaim = %q, want finished", gotTask.Status)
	}
}

func TestFaultAsyncReconcileFailsClosedOnCorruptStore(t *testing.T) {
	e := newAsyncFaultEnv(t)
	e.seedAsyncTask("do the thing")

	corrupt := filepath.Join(e.app.cfg.DataRoot(), "deliveries", "corrupt.json")
	if err := os.WriteFile(corrupt, []byte("{not valid json"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := e.app.reconcileDeliveries(context.Background()); err == nil {
		t.Fatalf("reconcileDeliveries returned nil on a corrupt store; want fail-closed error")
	}
}

func TestFaultAsyncSuccessPublishesDelegateFinishedOnce(t *testing.T) {
	e := newAsyncFaultEnv(t)
	tk, d := e.seedAsyncTask("do the thing")

	events, cancel := e.app.Bus().Subscribe(16)
	defer cancel()

	claimed, fence := e.claim(d.ID)
	e.worker.run(context.Background(), claimed, fence)

	finished := 0
	for {
		select {
		case ev := <-events:
			if ev.Type == bus.DelegateFinished && ev.TaskID == tk.ID {
				finished++
			}
			continue
		default:
		}
		break
	}
	if finished != 1 {
		t.Fatalf("DelegateFinished events for task = %d, want exactly 1", finished)
	}
}

func TestFaultAsyncFailurePublishesNoDelegateFinished(t *testing.T) {
	e := newAsyncFaultEnv(t)
	e.exec = func(ctx context.Context, req AsyncTurnRequest) AsyncTurnResult {
		e.runs++
		return AsyncTurnResult{Err: errors.New("model exploded")}
	}
	tk, d := e.seedAsyncTask("do the thing")

	events, cancel := e.app.Bus().Subscribe(16)
	defer cancel()

	claimed, fence := e.claim(d.ID)
	e.worker.run(context.Background(), claimed, fence)

	for {
		select {
		case ev := <-events:
			if ev.Type == bus.DelegateFinished && ev.TaskID == tk.ID {
				t.Fatalf("headless failure published a DelegateFinished event; want none")
			}
			continue
		default:
		}
		break
	}
}

func TestFaultAsyncRetryStaleResetCannotClobberFinalized(t *testing.T) {
	e := newAsyncFaultEnv(t)
	tk, d := e.seedAsyncTask("do the thing")

	past := time.Now().Add(-2 * deliveryLeaseTTL)
	_, fenceA, err := e.app.Deliveries().ClaimNextInLane(d.SpaceID, d.ParentMessageID, d.AgentID, "owner-A", past, deliveryLeaseTTL)
	if err != nil {
		t.Fatalf("claim A: %v", err)
	}
	personas := e.app.fuzzyPersonaResolver()
	ph, _, err := e.app.Spaces().EnsureDeliveryPlaceholder(d.SpaceID, d.ID, d.AgentID, d.ParentMessageID, personas.Info)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.app.Deliveries().BindResultMessage(d.ID, fenceA, ph.ID, past); err != nil {
		t.Fatalf("bind A: %v", err)
	}

	claimedB, fenceB, err := e.app.Deliveries().ClaimNextInLane(d.SpaceID, d.ParentMessageID, d.AgentID, "owner-B", time.Now(), deliveryLeaseTTL)
	if err != nil {
		t.Fatalf("claim B: %v", err)
	}
	if !(fenceB.Version > fenceA.Version) {
		t.Fatalf("reclaim fence version %d not greater than A %d", fenceB.Version, fenceA.Version)
	}
	e.worker.run(context.Background(), claimedB, fenceB)

	got, err := e.app.Deliveries().Get(d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != delivery.StatusCompleted {
		t.Fatalf("delivery status after B run = %q, want completed", got.Status)
	}

	if _, err := e.app.Spaces().ResetDeliveryMessage(d.SpaceID, ph.ID, d.ID, fenceA.OwnerID, fenceA.Version, time.Now()); !errors.Is(err, space.ErrStaleDeliveryWrite) {
		t.Fatalf("stale-fence reset err = %v, want ErrStaleDeliveryWrite (a superseded retry must not silently succeed)", err)
	}

	after := e.placeholders(d.ID)
	if len(after) != 1 {
		t.Fatalf("placeholders after stale reset = %d, want exactly 1", len(after))
	}
	if after[0].Status == "pending" {
		t.Fatalf("finalized placeholder clobbered back to pending by a stale retry reset (Delivery=completed/Message=pending split-brain)")
	}
	if strings.TrimSpace(after[0].Content) == "" {
		t.Fatalf("finalized placeholder content erased by stale reset")
	}
	gotTask, err := e.app.Tasks().Get(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotTask.Status != task.StatusFinished {
		t.Fatalf("task status = %q, want finished", gotTask.Status)
	}
}

func TestFaultAsyncRetryResetsPlaceholderBeforeExec(t *testing.T) {
	e := newAsyncFaultEnv(t)
	e.exec = func(ctx context.Context, req AsyncTurnRequest) AsyncTurnResult {
		e.runs++
		return AsyncTurnResult{Err: errors.New("transient failure")}
	}
	_, d := e.seedAsyncTask("do the thing")

	claimed, fence := e.claim(d.ID)
	e.worker.run(context.Background(), claimed, fence)
	failedPH := e.placeholders(claimed.ID)
	if len(failedPH) != 1 || failedPH[0].Status != "failed" {
		t.Fatalf("after failure placeholders = %+v, want exactly 1 failed", failedPH)
	}

	if _, err := e.app.RetryAsyncDelegate(claimed.ID); err != nil {
		t.Fatalf("retry: %v", err)
	}
	requeued, err := e.app.Deliveries().Get(claimed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if requeued.Status != delivery.StatusPending {
		t.Fatalf("delivery after retry = %q, want pending", requeued.Status)
	}
	if beforeClaim := e.placeholders(claimed.ID); beforeClaim[0].Status != "failed" {
		t.Fatalf("placeholder after retry-only = %q, want still failed (reset is the reclaiming worker's fenced job, not retry's)", beforeClaim[0].Status)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	e.exec = func(ctx context.Context, req AsyncTurnRequest) AsyncTurnResult {
		e.runs++
		close(entered)
		<-release
		return AsyncTurnResult{Content: "recovered reply", Steps: []task.KeyStep{{Kind: task.KindSummary, Title: "done", At: time.Now(), OK: true}}}
	}

	claimed2, fence2 := e.claim(claimed.ID)
	done := make(chan struct{})
	go func() {
		e.worker.run(context.Background(), claimed2, fence2)
		close(done)
	}()
	<-entered
	mid := e.placeholders(claimed.ID)
	if len(mid) != 1 {
		t.Fatalf("placeholders mid-exec = %d, want 1", len(mid))
	}
	if mid[0].Status != "pending" {
		t.Fatalf("placeholder mid-exec = %q, want pending (claim-time reset must precede exec, so claim->pending is authoritative)", mid[0].Status)
	}
	if mid[0].Error != "" {
		t.Fatalf("placeholder error mid-exec = %q, want cleared at claim time", mid[0].Error)
	}
	close(release)
	<-done

	final := e.placeholders(claimed.ID)
	if len(final) != 1 || strings.TrimSpace(final[0].Content) != "recovered reply" {
		t.Fatalf("final placeholder = %+v, want single recovered reply", final)
	}
}

func TestFaultAsyncStaleWorkerCannotExecuteOrFinalize(t *testing.T) {
	e := newAsyncFaultEnv(t)
	tk, d := e.seedAsyncTask("do the thing")

	past := time.Now().Add(-2 * deliveryLeaseTTL)
	_, fenceA, err := e.app.Deliveries().ClaimNextInLane(d.SpaceID, d.ParentMessageID, d.AgentID, "owner-A", past, deliveryLeaseTTL)
	if err != nil {
		t.Fatalf("claim A: %v", err)
	}
	personas := e.app.fuzzyPersonaResolver()
	ph, _, err := e.app.Spaces().EnsureDeliveryPlaceholder(d.SpaceID, d.ID, d.AgentID, d.ParentMessageID, personas.Info)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.app.Deliveries().BindResultMessage(d.ID, fenceA, ph.ID, past); err != nil {
		t.Fatalf("bind A: %v", err)
	}

	if _, _, err := e.app.Deliveries().ClaimNextInLane(d.SpaceID, d.ParentMessageID, d.AgentID, "owner-B", time.Now(), deliveryLeaseTTL); err != nil {
		t.Fatalf("claim B: %v", err)
	}

	claimedA, err := e.app.Deliveries().Get(d.ID)
	if err != nil {
		t.Fatal(err)
	}
	runsBefore := e.runs
	e.worker.run(context.Background(), claimedA, fenceA)

	if e.runs != runsBefore {
		t.Fatalf("stale-fence worker executed the turn (runs %d -> %d); a failed reset must abort before exec", runsBefore, e.runs)
	}
	got, err := e.app.Deliveries().Get(d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status == delivery.StatusCompleted {
		t.Fatalf("stale-fence worker completed the delivery; a failed reset must not silently succeed")
	}
	after := e.placeholders(d.ID)
	if len(after) != 1 {
		t.Fatalf("placeholders after stale run = %d, want exactly 1", len(after))
	}
	gotTask, err := e.app.Tasks().Get(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotTask.Status == task.StatusFinished {
		t.Fatalf("stale-fence worker finished the task; a failed reset must abort")
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
