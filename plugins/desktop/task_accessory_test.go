package desktop

import (
	"testing"
	"time"

	"github.com/abcdlsj/sumi/space"
	taskpkg "github.com/abcdlsj/sumi/task"
)

func TestTaskAccessoryAttachedToTriggerMessage(t *testing.T) {
	_, a := newThreadBackend(t)
	sp := mustChannel(t, a)
	root, err := a.Spaces().AppendUserMessage(sp.ID, "kick", nil)
	if err != nil {
		t.Fatal(err)
	}
	tk, err := a.Tasks().Create(taskpkg.CreateTaskInput{
		SpaceID: sp.ID, TriggerMessageID: root.ID, InitiatorID: "user", WorkerID: "coder", Title: "do",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Tasks().Update(tk.ID, taskpkg.UpdateTaskInput{Status: taskpkg.StatusRunning}); err != nil {
		t.Fatal(err)
	}
	loaded, _ := a.Spaces().LoadSpace(sp.ID)
	views := spaceMessagesToView(loaded, a)
	var rootView *MessageView
	for i := range views {
		if views[i].ID == root.ID {
			rootView = &views[i]
		}
	}
	if rootView == nil {
		t.Fatal("root view missing")
	}
	if rootView.TaskAccessory == nil {
		t.Fatal("expected TaskAccessory on trigger message")
	}
	if rootView.TaskAccessory.WorkerID != "coder" {
		t.Fatalf("worker = %q, want coder", rootView.TaskAccessory.WorkerID)
	}
	if rootView.TaskAccessory.WorkerDisplay != "Coder" {
		t.Fatalf("display = %q, want Coder", rootView.TaskAccessory.WorkerDisplay)
	}
	if rootView.TaskAccessory.Status != "running" {
		t.Fatalf("status = %q, want running", rootView.TaskAccessory.Status)
	}
	if rootView.TaskAccessory.Terminal {
		t.Fatal("running task is not terminal")
	}
}

func TestTaskAccessoryNonTriggerMessageHasNone(t *testing.T) {
	_, a := newThreadBackend(t)
	sp := mustChannel(t, a)
	root, err := a.Spaces().AppendUserMessage(sp.ID, "first", nil)
	if err != nil {
		t.Fatal(err)
	}
	other, err := a.Spaces().AppendUserMessage(sp.ID, "unrelated", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Tasks().Create(taskpkg.CreateTaskInput{
		SpaceID: sp.ID, TriggerMessageID: root.ID, InitiatorID: "user", WorkerID: "coder", Title: "x",
	}); err != nil {
		t.Fatal(err)
	}
	loaded, _ := a.Spaces().LoadSpace(sp.ID)
	views := spaceMessagesToView(loaded, a)
	for _, v := range views {
		if v.ID == other.ID && v.TaskAccessory != nil {
			t.Fatalf("non-trigger message must not get accessory: %+v", v.TaskAccessory)
		}
	}
}

func TestTaskAccessoryTerminalStatesPersist(t *testing.T) {
	_, a := newThreadBackend(t)
	sp := mustChannel(t, a)
	root, err := a.Spaces().AppendUserMessage(sp.ID, "kick", nil)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		status   taskpkg.Status
		uiToken  string
		terminal bool
	}{
		{taskpkg.StatusFinished, "finished", true},
		{taskpkg.StatusFailed, "failed", true},
		{taskpkg.StatusCanceled, "canceled", true},
		{taskpkg.StatusEmptyOutput, "no_output", true},
		{taskpkg.StatusRunning, "running", false},
		{taskpkg.StatusQueued, "queued", false},
	}
	for _, c := range cases {
		t.Run(string(c.status), func(t *testing.T) {
			tk, err := a.Tasks().Create(taskpkg.CreateTaskInput{
				SpaceID: sp.ID, TriggerMessageID: root.ID, InitiatorID: "user", WorkerID: "coder", Title: "x",
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := a.Tasks().Update(tk.ID, taskpkg.UpdateTaskInput{Status: c.status, Outcome: "x"}); err != nil {
				t.Fatal(err)
			}
			loaded, _ := a.Spaces().LoadSpace(sp.ID)
			views := spaceMessagesToView(loaded, a)
			var rootView *MessageView
			for i := range views {
				if views[i].ID == root.ID {
					rootView = &views[i]
				}
			}
			if rootView == nil || rootView.TaskAccessory == nil {
				t.Fatalf("accessory missing for %v", c.status)
			}
			if rootView.TaskAccessory.Status != c.uiToken {
				t.Fatalf("status = %q, want %q", rootView.TaskAccessory.Status, c.uiToken)
			}
			if rootView.TaskAccessory.Terminal != c.terminal {
				t.Fatalf("terminal = %v, want %v", rootView.TaskAccessory.Terminal, c.terminal)
			}
		})
	}
}

func TestTaskAccessoryFailedCarriesShortOutcome(t *testing.T) {
	_, a := newThreadBackend(t)
	sp := mustChannel(t, a)
	root, err := a.Spaces().AppendUserMessage(sp.ID, "kick", nil)
	if err != nil {
		t.Fatal(err)
	}
	tk, err := a.Tasks().Create(taskpkg.CreateTaskInput{
		SpaceID: sp.ID, TriggerMessageID: root.ID, InitiatorID: "user", WorkerID: "coder", Title: "x",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Tasks().Update(tk.ID, taskpkg.UpdateTaskInput{
		Status: taskpkg.StatusFailed, Outcome: "boom: connection refused",
	}); err != nil {
		t.Fatal(err)
	}
	loaded, _ := a.Spaces().LoadSpace(sp.ID)
	views := spaceMessagesToView(loaded, a)
	for _, v := range views {
		if v.ID == root.ID {
			if v.TaskAccessory == nil {
				t.Fatal("accessory missing")
			}
			if v.TaskAccessory.ShortOutcome != "boom: connection refused" {
				t.Fatalf("short_outcome = %q", v.TaskAccessory.ShortOutcome)
			}
		}
	}
}

func TestTaskAccessoryFinishedHasNoShortOutcome(t *testing.T) {
	_, a := newThreadBackend(t)
	sp := mustChannel(t, a)
	root, err := a.Spaces().AppendUserMessage(sp.ID, "kick", nil)
	if err != nil {
		t.Fatal(err)
	}
	tk, err := a.Tasks().Create(taskpkg.CreateTaskInput{
		SpaceID: sp.ID, TriggerMessageID: root.ID, InitiatorID: "user", WorkerID: "coder", Title: "x",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Tasks().Update(tk.ID, taskpkg.UpdateTaskInput{
		Status: taskpkg.StatusFinished, Outcome: "all done long form output",
	}); err != nil {
		t.Fatal(err)
	}
	loaded, _ := a.Spaces().LoadSpace(sp.ID)
	views := spaceMessagesToView(loaded, a)
	for _, v := range views {
		if v.ID == root.ID && v.TaskAccessory != nil {
			if v.TaskAccessory.ShortOutcome != "" {
				t.Fatalf("finished accessory must not show outcome body, got %q", v.TaskAccessory.ShortOutcome)
			}
		}
	}
}

func TestTaskAccessoryPicksLatestPerTrigger(t *testing.T) {
	_, a := newThreadBackend(t)
	sp := mustChannel(t, a)
	root, err := a.Spaces().AppendUserMessage(sp.ID, "kick", nil)
	if err != nil {
		t.Fatal(err)
	}
	old, err := a.Tasks().Create(taskpkg.CreateTaskInput{
		SpaceID: sp.ID, TriggerMessageID: root.ID, InitiatorID: "user", WorkerID: "coder", Title: "first",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Tasks().Update(old.ID, taskpkg.UpdateTaskInput{Status: taskpkg.StatusFinished}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	newer, err := a.Tasks().Create(taskpkg.CreateTaskInput{
		SpaceID: sp.ID, TriggerMessageID: root.ID, InitiatorID: "user", WorkerID: "reviewer", Title: "second",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Tasks().Update(newer.ID, taskpkg.UpdateTaskInput{Status: taskpkg.StatusRunning}); err != nil {
		t.Fatal(err)
	}
	loaded, _ := a.Spaces().LoadSpace(sp.ID)
	views := spaceMessagesToView(loaded, a)
	for _, v := range views {
		if v.ID == root.ID {
			if v.TaskAccessory == nil {
				t.Fatal("accessory missing")
			}
			if v.TaskAccessory.TaskID != newer.ID {
				t.Fatalf("expected latest task, got task %q (latest = %q)", v.TaskAccessory.TaskID, newer.ID)
			}
			if v.TaskAccessory.WorkerID != "reviewer" {
				t.Fatalf("worker = %q, want reviewer (latest)", v.TaskAccessory.WorkerID)
			}
		}
	}
}

func TestTaskAccessoryAgentDMSpacesGetNone(t *testing.T) {
	_, a := newThreadBackend(t)
	dm, err := a.Spaces().EnsureSpace(space.KindAgentDM, "coder", space.PersonaInfo{ID: "coder", Display: "Coder"})
	if err != nil {
		t.Fatal(err)
	}
	user, err := a.Spaces().AppendUserMessage(dm.ID, "hi", nil)
	if err != nil {
		t.Fatal(err)
	}
	// Even if a task were somehow associated with an AgentDM trigger
	// (collab tools never do this in practice), we must skip the
	// accessory projection.
	if _, err := a.Tasks().Create(taskpkg.CreateTaskInput{
		SpaceID: dm.ID, TriggerMessageID: user.ID, InitiatorID: "user", WorkerID: "coder", Title: "x",
	}); err != nil {
		t.Fatal(err)
	}
	loaded, _ := a.Spaces().LoadSpace(dm.ID)
	views := spaceMessagesToView(loaded, a)
	for _, v := range views {
		if v.TaskAccessory != nil {
			t.Fatalf("AgentDM Space must not surface task accessory, got %+v", v.TaskAccessory)
		}
	}
}
