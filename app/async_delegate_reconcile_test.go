package app

import (
	"context"
	"testing"
	"time"

	"github.com/abcdlsj/sumi/delivery"
	taskpkg "github.com/abcdlsj/sumi/task"
)

func (e *faultEnv) seedAsyncTask(status taskpkg.Status) *taskpkg.Task {
	e.t.Helper()
	origin, err := e.app.Spaces().AppendUserMessage(e.channel.ID, "please delegate", nil)
	if err != nil {
		e.t.Fatal(err)
	}
	tk, err := e.app.Tasks().Create(taskpkg.CreateTaskInput{
		SpaceID:          e.channel.ID,
		TriggerMessageID: origin.ID,
		InitiatorID:      "user",
		WorkerID:         "bob",
		Title:            "delegated subtask",
		Source:           "desktop:channel:work",
		ExecutionIntent: &taskpkg.ExecutionIntent{
			Input:        "do the thing",
			Runtime:      "stub",
			ShareContext: true,
		},
	})
	if err != nil {
		e.t.Fatal(err)
	}
	if status != "" && status != taskpkg.StatusQueued {
		if _, err := e.app.Tasks().Update(tk.ID, taskpkg.UpdateTaskInput{Status: status}); err != nil {
			e.t.Fatal(err)
		}
		tk, err = e.app.Tasks().Get(tk.ID)
		if err != nil {
			e.t.Fatal(err)
		}
	}
	return tk
}

func (e *faultEnv) asyncDeliveries(spaceID string) []*delivery.Delivery {
	e.t.Helper()
	all, err := e.app.Deliveries().ListBySpace(spaceID)
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

func TestFaultAsyncTaskExistsDeliveryMissing(t *testing.T) {
	e := newFaultEnv(t)
	tk := e.seedAsyncTask(taskpkg.StatusQueued)

	if ds := e.asyncDeliveries(e.channel.ID); len(ds) != 0 {
		t.Fatalf("precondition: expected 0 async deliveries, got %d", len(ds))
	}

	if err := e.app.reconcileDeliveries(context.Background()); err != nil {
		t.Fatal(err)
	}
	ds := e.asyncDeliveries(e.channel.ID)
	if len(ds) != 1 {
		t.Fatalf("reconcile did not rebuild the async Delivery from the Task fact: %d async deliveries, want 1", len(ds))
	}
	d := ds[0]
	if d.TaskID != tk.ID {
		t.Fatalf("rebuilt delivery.TaskID = %q, want %q", d.TaskID, tk.ID)
	}
	if d.OriginMessageID != tk.ID {
		t.Fatalf("rebuilt delivery.OriginMessageID = %q, want Task.ID %q", d.OriginMessageID, tk.ID)
	}
	if d.ParentMessageID != tk.TriggerMessageID {
		t.Fatalf("rebuilt delivery.ParentMessageID = %q, want trigger %q", d.ParentMessageID, tk.TriggerMessageID)
	}
	if d.AgentID != tk.WorkerID || d.SpaceID != tk.SpaceID {
		t.Fatalf("rebuilt delivery = {space:%q agent:%q}, want {%q %q}", d.SpaceID, d.AgentID, tk.SpaceID, tk.WorkerID)
	}
	if d.Status != delivery.StatusPending {
		t.Fatalf("rebuilt delivery status = %q, want pending", d.Status)
	}
}

func TestFaultAsyncReconcileDistinctTasksSameTrigger(t *testing.T) {
	e := newFaultEnv(t)
	trigger, err := e.app.Spaces().AppendUserMessage(e.channel.ID, "please delegate twice", nil)
	if err != nil {
		t.Fatal(err)
	}
	mk := func(input string) *taskpkg.Task {
		tk, err := e.app.Tasks().Create(taskpkg.CreateTaskInput{
			SpaceID:          e.channel.ID,
			TriggerMessageID: trigger.ID,
			InitiatorID:      "user",
			WorkerID:         "bob",
			Title:            "sub " + input,
			Source:           "desktop:channel:work",
			ExecutionIntent:  &taskpkg.ExecutionIntent{Input: input, Runtime: "stub"},
		})
		if err != nil {
			t.Fatal(err)
		}
		return tk
	}
	t1 := mk("first job")
	t2 := mk("second job")

	if err := e.app.reconcileDeliveries(context.Background()); err != nil {
		t.Fatal(err)
	}
	ds := e.asyncDeliveries(e.channel.ID)
	if len(ds) != 2 {
		t.Fatalf("distinct Tasks on same trigger = %d async deliveries, want 2 (no collision)", len(ds))
	}
	seen := map[string]bool{}
	for _, d := range ds {
		seen[d.TaskID] = true
	}
	if !seen[t1.ID] || !seen[t2.ID] {
		t.Fatalf("deliveries do not reference both Tasks: got %v, want {%q,%q}", seen, t1.ID, t2.ID)
	}
}

func TestFaultAsyncReconcileIdempotent(t *testing.T) {
	e := newFaultEnv(t)
	e.seedAsyncTask(taskpkg.StatusQueued)

	for i := 0; i < 3; i++ {
		if err := e.app.reconcileDeliveries(context.Background()); err != nil {
			t.Fatalf("reconcile #%d: %v", i, err)
		}
	}
	if ds := e.asyncDeliveries(e.channel.ID); len(ds) != 1 {
		t.Fatalf("reconcile not idempotent: %d async deliveries, want exactly 1 (create-if-absent)", len(ds))
	}
}

func TestFaultAsyncReconcileRunningNotDuplicated(t *testing.T) {
	e := newFaultEnv(t)
	e.seedAsyncTask(taskpkg.StatusRunning)

	if err := e.app.reconcileDeliveries(context.Background()); err != nil {
		t.Fatal(err)
	}
	ds := e.asyncDeliveries(e.channel.ID)
	if len(ds) != 1 {
		t.Fatalf("first reconcile: %d async deliveries, want 1", len(ds))
	}
	claimed, _, err := e.app.Deliveries().ClaimNextInLane(ds[0].SpaceID, ds[0].ParentMessageID, ds[0].AgentID, e.worker.ownerID, time.Now(), deliveryLeaseTTL)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claimed == nil {
		t.Fatal("claim returned nil for a pending async delivery")
	}

	if err := e.app.reconcileDeliveries(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ds := e.asyncDeliveries(e.channel.ID); len(ds) != 1 {
		t.Fatalf("reconcile duplicated a live-leased async Delivery: %d, want 1", len(ds))
	}
}

func TestFaultAsyncReconcileSkipsTerminalTask(t *testing.T) {
	for _, st := range []taskpkg.Status{taskpkg.StatusFinished, taskpkg.StatusFailed, taskpkg.StatusCanceled} {
		t.Run(string(st), func(t *testing.T) {
			e := newFaultEnv(t)
			e.seedAsyncTask(st)

			if err := e.app.reconcileDeliveries(context.Background()); err != nil {
				t.Fatal(err)
			}
			if ds := e.asyncDeliveries(e.channel.ID); len(ds) != 0 {
				t.Fatalf("reconcile resurrected a %s Task: %d async deliveries, want 0", st, len(ds))
			}
		})
	}
}

func TestFaultAsyncReconcileSkipsTaskWithoutIntent(t *testing.T) {
	e := newFaultEnv(t)
	origin, err := e.app.Spaces().AppendUserMessage(e.channel.ID, "board item", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.app.Tasks().Create(taskpkg.CreateTaskInput{
		SpaceID:          e.channel.ID,
		TriggerMessageID: origin.ID,
		InitiatorID:      "user",
		WorkerID:         "bob",
		Title:            "no-intent board task",
		Source:           "desktop:channel:work",
	}); err != nil {
		t.Fatal(err)
	}
	if err := e.app.reconcileDeliveries(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ds := e.asyncDeliveries(e.channel.ID); len(ds) != 0 {
		t.Fatalf("reconcile built a Delivery for an intent-less Task: %d, want 0", len(ds))
	}
}
