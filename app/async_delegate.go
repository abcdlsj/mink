package app

import (
	"strings"
	"time"

	"github.com/abcdlsj/sumi/delivery"
	"github.com/abcdlsj/sumi/task"
)

func deliveryFromTask(tk *task.Task) *delivery.Delivery {
	if tk == nil || tk.ExecutionIntent == nil {
		return nil
	}
	spaceID := strings.TrimSpace(tk.SpaceID)
	agentID := strings.TrimSpace(tk.WorkerID)
	taskID := strings.TrimSpace(tk.ID)
	if spaceID == "" || agentID == "" || taskID == "" {
		return nil
	}
	if tk.Status.Terminal() {
		return nil
	}
	return &delivery.Delivery{
		Kind:            delivery.KindAsyncDelegate,
		SpaceID:         spaceID,
		ParentMessageID: strings.TrimSpace(tk.TriggerMessageID),
		OriginMessageID: taskID,
		AgentID:         agentID,
		TaskID:          taskID,
	}
}

func (a *App) EnqueueAsyncDelegate(tk *task.Task) (*delivery.Delivery, error) {
	if a == nil || a.store == nil {
		return nil, nil
	}
	d := deliveryFromTask(tk)
	if d == nil {
		return nil, nil
	}
	created, _, err := a.store.Deliveries().CreateIfAbsent(d, time.Now())
	if err != nil {
		return nil, err
	}
	if a.worker != nil {
		a.worker.wake()
	}
	return created, nil
}
