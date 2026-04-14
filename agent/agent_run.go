package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/msg"
)

func (a *Agent) Run(ctx context.Context, src, input string) error {
	a.applyStreamForSource(src)
	return a.run(ctx, src, "user", input)
}

func (a *Agent) RunSystem(ctx context.Context, src, input string) error {
	a.applyStreamForSource(src)
	return a.run(ctx, src, "system", input)
}

func (a *Agent) applyStreamForSource(src string) {
	if strings.HasPrefix(src, "telegram:") {
		a.stream = a.cfg.TelegramStream
	} else {
		a.stream = a.cfg.Stream
	}
}

func (a *Agent) run(ctx context.Context, src, role, input string) (retErr error) {
	a.logSessionStart(src)
	defer a.logSessionEnd()
	defer a.flushSession(&retErr)

	ctx, cancel := a.prepareRunContext(ctx, src)
	defer cancel()
	a.maybeAutoCompact(ctx, src)
	ctx, cancel = a.withAgentTimeout(ctx)
	defer cancel()

	a.session.Add(msg.Message{Role: role, Content: input})
	a.resetTurnState()
	a.logUserInput(role, input)
	a.appendEvent(ctx, "input.received", role, map[string]any{"content": input})

	if a.bus != nil {
		go a.watchInterrupt(ctx)
	}

	return a.runSteps(ctx, src)
}

func (a *Agent) flushSession(retErr *error) {
	if err := a.session.Flush(); err != nil {
		a.logWarn("session_flush_error", map[string]any{"error": err.Error()})
		if *retErr == nil {
			*retErr = err
			return
		}
		*retErr = fmt.Errorf("%w; flush: %v", *retErr, err)
	}
}

func (a *Agent) prepareRunContext(ctx context.Context, src string) (context.Context, context.CancelFunc) {
	ctx = bus.WithSource(ctx, src)
	a.ResetInterrupt()

	ctx, cancel := context.WithCancel(ctx)
	a.mu.Lock()
	a.cancelFn = cancel
	a.mu.Unlock()
	return ctx, cancel
}

func (a *Agent) withAgentTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := time.Duration(a.cfg.Timeout.Agent) * time.Second
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

func (a *Agent) runSteps(ctx context.Context, src string) error {
	maxSteps := a.cfg.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 100
	}

	for i := 0; i < maxSteps; i++ {
		if err := a.checkRunState(ctx); err != nil {
			return err
		}
		done, err := a.runSingleStep(ctx, src, i)
		if err != nil {
			return a.normalizeRunError(ctx, err)
		}
		if done {
			return nil
		}
	}
	return fmt.Errorf("max steps reached: %d", maxSteps)
}

func (a *Agent) checkRunState(ctx context.Context) error {
	if a.IsInterrupted() {
		return a.finishInterruptedRun()
	}
	if err := ctx.Err(); err != nil {
		if a.IsInterrupted() || err == context.Canceled {
			return a.finishInterruptedRun()
		}
		return fmt.Errorf("agent timeout: %w", err)
	}
	return nil
}

func (a *Agent) runSingleStep(ctx context.Context, src string, stepNum int) (bool, error) {
	stepStart := time.Now()
	a.logStepStart(stepNum)
	done, err := a.step(ctx, src, stepNum)
	a.logStepEnd(stepNum, time.Since(stepStart), err)
	return done, err
}

func (a *Agent) normalizeRunError(ctx context.Context, err error) error {
	if a.IsInterrupted() || ctx.Err() == context.Canceled {
		return a.finishInterruptedRun()
	}
	return err
}

func (a *Agent) finishInterruptedRun() error {
	a.logInterrupt("user interrupted")
	a.session.Add(msg.Message{Role: "system", Content: "[User interrupted]"})
	return nil
}

func (a *Agent) watchInterrupt(ctx context.Context) {
	if a.bus == nil {
		return
	}

	ch := make(chan bus.Msg, 1)
	a.bus.Subscribe(bus.TypeInterrupt, ch)
	defer a.bus.Unsubscribe(bus.TypeInterrupt, ch)

	for {
		select {
		case m := <-ch:
			if m.To == a.id || m.To == bus.AddrBroadcast {
				a.Interrupt()
				return
			}
		case <-ctx.Done():
			return
		}
	}
}
