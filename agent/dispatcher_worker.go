package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/abcdlsj/mink/bus"
)

const workerIdleTTL = 5 * time.Minute

const (
	workerEnqueueTimeout     = 2 * time.Second
	workerTaskEnqueueTimeout = 8 * time.Second
	workerStatusFirstDelay   = 12 * time.Second
)

type workerState struct {
	q      chan bus.Msg
	cancel context.CancelFunc
}

func (d *Dispatcher) worker(ctx context.Context, src string, q chan bus.Msg) {
	defer func() {
		if r := recover(); r != nil {
			d.pub(bus.Msg{
				Type:    bus.TypeAssistant,
				From:    d.agentID,
				To:      src,
				Payload: fmt.Sprintf("error: worker panic: %v", r),
			})
			d.pub(bus.Msg{
				Type: bus.TypeTurnDone,
				From: d.agentID,
				To:   src,
			})
			d.removeWorker(src)
		}
	}()

	idle := time.NewTimer(workerIdleTTL)
	defer idle.Stop()

	for {
		select {
		case m := <-q:
			if !idle.Stop() {
				select {
				case <-idle.C:
				default:
				}
			}
			idle.Reset(workerIdleTTL)

			a := d.getOrCreateAgent(src)
			initialInput, ok := m.Payload.(string)
			if !ok {
				d.pub(bus.Msg{
					Type:    bus.TypeAssistant,
					From:    d.agentID,
					Payload: "error: invalid input payload",
					To:      src,
				})
				d.pub(bus.Msg{
					Type: bus.TypeTurnDone,
					From: d.agentID,
					To:   src,
				})
				continue
			}

			speakerID, err := d.runSourceTurn(ctx, src, m.Type, initialInput, a)
			if err != nil {
				if speakerID == "" {
					speakerID = d.agentID
				}
				d.pub(bus.Msg{
					Type:    bus.TypeAssistant,
					From:    speakerID,
					Payload: fmt.Sprintf("error: %v", err),
					To:      src,
				})
			}
			d.pub(bus.Msg{
				Type: bus.TypeTurnDone,
				From: d.agentID,
				To:   src,
			})
		case <-idle.C:
			d.removeWorker(src)
			return
		case <-ctx.Done():
			d.removeWorker(src)
			return
		}
	}
}

func (d *Dispatcher) ensureWorker(parentCtx context.Context, src string) *workerState {
	d.mu.Lock()
	defer d.mu.Unlock()

	if w, ok := d.workers[src]; ok {
		return w
	}

	ctx, cancel := context.WithCancel(parentCtx)
	w := &workerState{
		q:      make(chan bus.Msg, 10),
		cancel: cancel,
	}
	d.workers[src] = w
	go d.worker(ctx, src, w.q)
	return w
}

func (d *Dispatcher) removeWorker(src string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.workers, src)
}

func enqueueWorker(parent context.Context, q chan bus.Msg, m bus.Msg, timeout time.Duration) bool {
	if timeout <= 0 {
		timeout = workerEnqueueTimeout
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case q <- m:
		return true
	case <-parent.Done():
		return false
	case <-timer.C:
		return false
	}
}

func (d *Dispatcher) runWithStatus(ctx context.Context, src, msgType, in string, a *Agent) error {
	errCh := make(chan error, 1)
	go func() {
		if msgType == bus.TypeTaskDone {
			errCh <- a.RunSystem(ctx, src, in)
			return
		}
		errCh <- a.Run(ctx, src, in)
	}()

	first := time.NewTimer(workerStatusFirstDelay)
	defer first.Stop()

	for {
		select {
		case err := <-errCh:
			return err
		case <-ctx.Done():
			return ctx.Err()
		case <-first.C:
			if msgType == bus.TypeTaskDone {
				continue
			}
			d.pub(bus.Msg{
				Type:    bus.TypeAssistant,
				From:    d.agentID,
				To:      src,
				Payload: "[status] still working, please wait...",
			})
		}
	}
}
