package cli

import (
	"context"
	"sync"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/abcdlsj/mink/tool"
)

type shellApprover struct {
	mu   sync.Mutex
	next int
	prog *tea.Program
}

type approvalRequest struct {
	id   int
	req  tool.Request
	resp chan tool.Approval
}

func newShellApprover() *shellApprover {
	return &shellApprover{}
}

func (a *shellApprover) attach(p *tea.Program) {
	a.mu.Lock()
	a.prog = p
	a.mu.Unlock()
}

func (a *shellApprover) Approve(ctx context.Context, req tool.Request) (tool.Approval, error) {
	ar := &approvalRequest{
		id:   a.allocID(),
		req:  req,
		resp: make(chan tool.Approval, 1),
	}
	a.send(shellApprovalEnqueuedMsg{Request: ar})
	defer a.send(shellApprovalDroppedMsg{ID: ar.id})

	select {
	case v := <-ar.resp:
		return v, nil
	case <-ctx.Done():
		return tool.Denied, ctx.Err()
	}
}

func (a *shellApprover) allocID() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	id := a.next
	a.next++
	return id
}

func (a *shellApprover) send(msg tea.Msg) {
	a.mu.Lock()
	p := a.prog
	a.mu.Unlock()
	if p != nil {
		p.Send(msg)
	}
}
