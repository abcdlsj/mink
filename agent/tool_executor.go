package agent

import (
	"context"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/msg"
)

type toolExecutor struct {
	tools ToolRunner
	sink  turnSink
}

func (x toolExecutor) run(ctx context.Context, t *Turn, calls []msg.ToolCall) {
	for _, call := range calls {
		x.runOne(ctx, t, call)
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
