package app

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rivo/tview"

	"github.com/abcdlsj/mink/tool"
)

type tuiApprover struct {
	ui *tui

	mu      sync.Mutex
	nextID  int
	pending []*approvalRequest
}

type approvalRequest struct {
	id   int
	req  tool.Request
	resp chan tool.Approval
}

func newTUIApprover(ui *tui) *tuiApprover {
	return &tuiApprover{ui: ui}
}

func (a *tuiApprover) Approve(ctx context.Context, req tool.Request) (tool.Approval, error) {
	ar := &approvalRequest{
		id:   a.allocID(),
		req:  req,
		resp: make(chan tool.Approval, 1),
	}
	a.ui.tv.QueueUpdateDraw(func() {
		a.enqueue(ar)
	})
	defer a.ui.tv.QueueUpdateDraw(func() {
		a.drop(ar.id)
	})

	select {
	case v := <-ar.resp:
		return v, nil
	case <-ctx.Done():
		return tool.Denied, ctx.Err()
	}
}

func (a *tuiApprover) allocID() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	id := a.nextID
	a.nextID++
	return id
}

func (a *tuiApprover) pendingCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.pending)
}

func (a *tuiApprover) enqueue(req *approvalRequest) {
	a.mu.Lock()
	a.pending = append(a.pending, req)
	a.mu.Unlock()
	a.showNext()
	a.ui.refreshStatus()
}

func (a *tuiApprover) drop(id int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := a.pending[:0]
	for _, req := range a.pending {
		if req.id != id {
			out = append(out, req)
		}
	}
	a.pending = out
	a.ui.pages.RemovePage("approval")
	a.showNext()
	a.ui.refreshStatus()
}

func (a *tuiApprover) showNext() {
	if a.ui.pages.HasPage("approval") {
		return
	}
	a.mu.Lock()
	if len(a.pending) == 0 {
		a.mu.Unlock()
		return
	}
	req := a.pending[0]
	a.mu.Unlock()

	text := fmt.Sprintf("Tool approval\n\n%s\n\npattern: %s\n\ntime: %s", req.req.Action, req.req.Pattern, time.Now().Format(time.RFC3339))
	modal := tview.NewModal().
		SetText(text).
		AddButtons([]string{"Deny", "Once", "Always"}).
		SetDoneFunc(func(buttonIndex int, buttonLabel string) {
			switch buttonIndex {
			case 1:
				req.resp <- tool.AllowOnce
			case 2:
				req.resp <- tool.AllowAlways
			default:
				req.resp <- tool.Denied
			}
			a.drop(req.id)
			a.ui.tv.SetFocus(a.ui.input)
		})
	a.ui.pages.AddPage("approval", modal, true, true)
	a.ui.tv.SetFocus(modal)
}
