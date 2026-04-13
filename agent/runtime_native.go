package agent

import (
	"context"
	"fmt"
	"sync"

	"github.com/abcdlsj/mink/msg"
	"github.com/abcdlsj/mink/session"
)

type NativeRuntime struct {
	agent  *Agent
	source string
	status RuntimeStatus
	mu     sync.Mutex
}

func NewNativeRuntime(a *Agent) *NativeRuntime {
	return &NativeRuntime{agent: a, status: RuntimeIdle}
}

func (r *NativeRuntime) Start(_ context.Context, cfg RuntimeConfig) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.status == RuntimeRunning {
		return fmt.Errorf("runtime already running")
	}
	r.source = cfg.Source
	if cfg.Session != nil {
		r.agent.session = cfg.Session
	}
	r.status = RuntimeIdle
	return nil
}

func (r *NativeRuntime) Send(ctx context.Context, input string) error {
	r.setStatus(RuntimeRunning)
	defer r.setStatus(RuntimeIdle)
	return r.agent.Run(ctx, r.source, input)
}

func (r *NativeRuntime) SendSystem(ctx context.Context, input string) error {
	r.setStatus(RuntimeRunning)
	defer r.setStatus(RuntimeIdle)
	return r.agent.RunSystem(ctx, r.source, input)
}

func (r *NativeRuntime) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.status = RuntimeStopped
	return nil
}

func (r *NativeRuntime) Status() RuntimeStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.status
}

func (r *NativeRuntime) Session() *session.Session {
	return r.agent.Session()
}

func (r *NativeRuntime) TokenUsage() msg.TokenUsage {
	return r.agent.TokenUsage()
}

func (r *NativeRuntime) Interrupt() {
	r.agent.Interrupt()
}

func (r *NativeRuntime) Agent() *Agent {
	return r.agent
}

func (r *NativeRuntime) setStatus(s RuntimeStatus) {
	r.mu.Lock()
	r.status = s
	r.mu.Unlock()
}
