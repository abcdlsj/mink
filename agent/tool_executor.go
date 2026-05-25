package agent

import (
	"context"
	"sync"

	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/msg"
)

type toolExecutor struct {
	tools ToolRunner
	sink  turnSink
}

func (x toolExecutor) run(ctx context.Context, t *Turn, calls []msg.ToolCall) {
	if parallelToolCalls(calls) {
		x.runParallel(ctx, t, calls)
		return
	}
	for _, call := range calls {
		x.runOne(ctx, t, call)
	}
}

type toolRunResult struct {
	i      int
	result msg.ToolResult
	ev     bus.Event
}

func (x toolExecutor) runParallel(ctx context.Context, t *Turn, calls []msg.ToolCall) {
	ch := make(chan toolRunResult, len(calls))
	var wg sync.WaitGroup
	for i, call := range calls {
		x.sink.Publish(bus.Event{
			Type:       bus.ToolCallStarted,
			ToolCallID: call.ID,
			Tool:       call.Name,
			Input:      string(call.Args),
		})
		wg.Add(1)
		go func(i int, call msg.ToolCall) {
			defer wg.Done()
			out, err := x.tools.Run(ctx, call.Name, call.Args)
			result := msg.ToolResult{ToolCallID: call.ID, Content: out}
			if err != nil {
				result.Error = err.Error()
			}
			ch <- toolRunResult{
				i:      i,
				result: result,
				ev: bus.Event{
					Type:       toolResultType(result),
					ToolCallID: call.ID,
					Tool:       call.Name,
					Input:      string(call.Args),
					Output:     out,
					Err:        result.Error,
				},
			}
		}(i, call)
	}
	go func() {
		wg.Wait()
		close(ch)
	}()

	results := make([]msg.ToolResult, len(calls))
	for r := range ch {
		results[r.i] = r.result
		x.sink.Publish(r.ev)
	}
	for _, result := range results {
		t.Session.Add(newToolMessage(result))
	}
}

func (x toolExecutor) runOne(ctx context.Context, t *Turn, call msg.ToolCall) {
	x.sink.Publish(bus.Event{
		Type:       bus.ToolCallStarted,
		ToolCallID: call.ID,
		Tool:       call.Name,
		Input:      string(call.Args),
	})

	out, err := x.tools.Run(ctx, call.Name, call.Args)
	result := msg.ToolResult{ToolCallID: call.ID, Content: out}
	if err != nil {
		result.Error = err.Error()
	}
	t.Session.Add(newToolMessage(result))

	x.sink.Publish(bus.Event{
		Type:       toolResultType(result),
		ToolCallID: call.ID,
		Tool:       call.Name,
		Input:      string(call.Args),
		Output:     out,
		Err:        result.Error,
	})
}

func toolResultType(result msg.ToolResult) string {
	if result.Error != "" {
		return bus.ToolCallFailed
	}
	return bus.ToolCallFinished
}

func parallelToolCalls(calls []msg.ToolCall) bool {
	if len(calls) < 2 {
		return false
	}
	for _, call := range calls {
		if call.Name != "read" {
			return false
		}
	}
	return true
}
