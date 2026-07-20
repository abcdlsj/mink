package app

import (
	"strings"
	"time"

	"github.com/abcdlsj/sumi/delivery"
	"github.com/abcdlsj/sumi/space"
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

func (a *App) RetryAsyncDelegate(deliveryID string) (bool, error) {
	if a == nil || a.store == nil {
		return false, nil
	}
	id := strings.TrimSpace(deliveryID)
	if id == "" {
		return false, nil
	}
	deliveries := a.store.Deliveries()
	d, err := deliveries.Get(id)
	if err != nil {
		return false, err
	}
	if d == nil || d.Kind != delivery.KindAsyncDelegate {
		return false, nil
	}
	if _, err := deliveries.Requeue(id, time.Now()); err != nil {
		return false, err
	}
	if a.spaces != nil && strings.TrimSpace(d.ResultMessageID) != "" {
		_, _ = a.spaces.UpdateMessage(d.SpaceID, d.ResultMessageID, func(m *space.Message) {
			m.Status = "pending"
			m.Error = ""
		})
	}
	if a.worker != nil {
		a.worker.wake()
	}
	return true, nil
}
