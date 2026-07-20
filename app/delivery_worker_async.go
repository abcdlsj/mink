package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/abcdlsj/sumi/delivery"
	"github.com/abcdlsj/sumi/space"
	"github.com/abcdlsj/sumi/task"
)

func (w *deliveryWorker) runAsyncDelegate(ctx context.Context, d *delivery.Delivery, fence delivery.Fence) {
	a := w.app
	deliveries := a.store.Deliveries()

	tk, ok := a.loadDeliveryTask(d)
	if !ok {
		now := w.now()
		if id := strings.TrimSpace(d.ResultMessageID); id != "" {
			if _, ferr := a.spaces.FailDeliveryMessage(d.SpaceID, id, d.ID, fence.OwnerID, fence.Version, now, "task not found"); ferr != nil {
				return
			}
		}
		_, _ = deliveries.Fail(d.ID, fence, "task not found", now)
		return
	}

	personas := a.fuzzyPersonaResolver()
	placeholder, _, err := a.spaces.EnsureDeliveryPlaceholder(d.SpaceID, d.ID, d.AgentID, d.ParentMessageID, personas.Info)
	if err != nil {
		_, _ = deliveries.Fail(d.ID, fence, "ensure placeholder: "+err.Error(), w.now())
		return
	}
	bound, _, err := deliveries.BindResultMessage(d.ID, fence, placeholder.ID, w.now())
	if err != nil {
		return
	}
	resultMessageID := bound.ResultMessageID

	worker := a.personas.Get(d.AgentID)
	if worker == nil {
		if _, ferr := a.spaces.FailDeliveryMessage(d.SpaceID, resultMessageID, d.ID, fence.OwnerID, fence.Version, w.now(), "worker persona not registered"); ferr == nil {
			_, _ = deliveries.Fail(d.ID, fence, "worker persona not registered", w.now())
		}
		return
	}
	run, err := a.tasks.StartRun(tk.ID)
	if err != nil {
		_, _ = deliveries.Fail(d.ID, fence, "start run: "+err.Error(), w.now())
		return
	}
	if _, err := a.tasks.Update(tk.ID, task.UpdateTaskInput{Status: task.StatusRunning}); err != nil {
		_, _ = deliveries.Fail(d.ID, fence, "mark running: "+err.Error(), w.now())
		return
	}

	exec := a.asyncTurnExecutor
	if exec == nil {
		w.failAsync(d, fence, tk, run.ID, resultMessageID, nil, errors.New("no async turn executor registered"))
		return
	}

	guard := &leaseGuard{}
	renewCtx, stopRenew := context.WithCancel(ctx)
	var renewWG sync.WaitGroup
	renewWG.Add(1)
	go func() {
		defer renewWG.Done()
		w.renewLoop(renewCtx, d.ID, fence, guard)
	}()

	result := exec(ctx, AsyncTurnRequest{
		Task:            tk,
		Worker:          worker,
		SpaceID:         d.SpaceID,
		ParentMessageID: d.ParentMessageID,
		AgentID:         d.AgentID,
		StreamID:        newStreamID(),
		DeliveryID:      d.ID,
		ResultMessageID: resultMessageID,
	})

	stopRenew()
	renewWG.Wait()

	if guard.stale() {
		return
	}

	if result.Err != nil {
		w.failAsync(d, fence, tk, run.ID, resultMessageID, result.Steps, result.Err)
		return
	}
	if result.EmptyOutput {
		w.failAsyncEmpty(d, fence, tk, run.ID, resultMessageID, result.Steps)
		return
	}

	now := w.now()
	written, _, err := a.spaces.FinalizeDeliveryMessage(d.SpaceID, resultMessageID, d.ID, fence.OwnerID, fence.Version, now, func(m *space.Message) {
		m.Content = result.Content
		m.Reasoning = result.Reasoning
		m.Mentions = result.Mentions
		m.Status = ""
		m.Error = ""
		m.Usage = result.Usage
		m.RuntimeMeta = result.RuntimeMeta
	}, nil, personas.Info, nil)
	if err != nil {
		if errors.Is(err, space.ErrStaleDeliveryWrite) {
			return
		}
		w.failAsync(d, fence, tk, run.ID, resultMessageID, result.Steps, err)
		return
	}

	outcome := shortOutcomeForTask(result.Content)
	if _, err := a.tasks.Update(tk.ID, task.UpdateTaskInput{
		Status:          task.StatusFinished,
		ResultMessageID: written.ID,
		Outcome:         outcome,
	}); err != nil {
		return
	}
	if _, err := a.tasks.FinishRun(run.ID, task.FinishRunInput{
		Status:   task.StatusFinished,
		KeySteps: result.Steps,
	}); err != nil {
		return
	}
	if _, err := deliveries.Complete(d.ID, fence, written.ID, now); err != nil {
		return
	}
}

func (w *deliveryWorker) failAsync(d *delivery.Delivery, fence delivery.Fence, tk *task.Task, runID, resultMessageID string, steps []task.KeyStep, cause error) {
	a := w.app
	now := w.now()
	msg := cause.Error()
	if _, ferr := a.spaces.FailDeliveryMessage(d.SpaceID, resultMessageID, d.ID, fence.OwnerID, fence.Version, now, msg); ferr != nil {
		return
	}
	if len(steps) == 0 {
		steps = []task.KeyStep{{Kind: task.KindError, Title: shortAsyncError(msg), At: now, OK: false}}
	}
	_, _ = a.tasks.Update(tk.ID, task.UpdateTaskInput{Status: task.StatusFailed, Outcome: shortAsyncError(msg)})
	_, _ = a.tasks.FinishRun(runID, task.FinishRunInput{Status: task.StatusFailed, KeySteps: steps})
	_, _ = a.store.Deliveries().Fail(d.ID, fence, msg, now)
}

func (w *deliveryWorker) failAsyncEmpty(d *delivery.Delivery, fence delivery.Fence, tk *task.Task, runID, resultMessageID string, steps []task.KeyStep) {
	a := w.app
	now := w.now()
	if _, ferr := a.spaces.FailDeliveryMessage(d.SpaceID, resultMessageID, d.ID, fence.OwnerID, fence.Version, now, "no output"); ferr != nil {
		return
	}
	_, _ = a.tasks.Update(tk.ID, task.UpdateTaskInput{Status: task.StatusEmptyOutput, Outcome: "no output"})
	_, _ = a.tasks.FinishRun(runID, task.FinishRunInput{Status: task.StatusEmptyOutput, KeySteps: steps})
	_, _ = a.store.Deliveries().Fail(d.ID, fence, "no output", now)
}

func (a *App) loadDeliveryTask(d *delivery.Delivery) (*task.Task, bool) {
	if a == nil || a.tasks == nil || d == nil {
		return nil, false
	}
	id := strings.TrimSpace(d.TaskID)
	if id == "" {
		return nil, false
	}
	tk, err := a.tasks.Get(id)
	if err != nil || tk == nil {
		return nil, false
	}
	if tk.ExecutionIntent == nil || tk.Status == task.StatusCanceled {
		return nil, false
	}
	return tk, true
}

func shortAsyncError(s string) string {
	s = strings.TrimSpace(s)
	const max = 120
	if rl := []rune(s); len(rl) > max {
		s = string(rl[:max])
	}
	return fmt.Sprintf("Failed: %s", s)
}
