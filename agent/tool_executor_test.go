package agent

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/abcdlsj/sumi/llm"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/session"
)

func TestToolExecutorRunsReadCallsInParallel(t *testing.T) {
	runner := &slowRunner{}
	tu := &Turn{Session: session.New("cli")}
	calls := []msg.ToolCall{
		{ID: "a", Name: "read", Args: json.RawMessage(`{"path":"a"}`)},
		{ID: "b", Name: "read", Args: json.RawMessage(`{"path":"b"}`)},
	}

	start := time.Now()
	toolExecutor{tools: runner}.run(context.Background(), tu, calls)
	elapsed := time.Since(start)
	if elapsed >= 180*time.Millisecond {
		t.Fatalf("read calls did not run in parallel, elapsed %s", elapsed)
	}
	if len(tu.Session.Messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(tu.Session.Messages))
	}
	if tu.Session.Messages[0].ToolResults[0].ToolCallID != "a" || tu.Session.Messages[1].ToolResults[0].ToolCallID != "b" {
		t.Fatalf("tool results not preserved in call order: %#v", tu.Session.Messages)
	}
}

func TestToolExecutorKeepsMixedCallsSerial(t *testing.T) {
	calls := []msg.ToolCall{
		{ID: "a", Name: "read"},
		{ID: "b", Name: "bash"},
	}
	if parallelToolCalls(calls) {
		t.Fatal("mixed tool calls must not run in parallel")
	}
}

type slowRunner struct {
	mu    sync.Mutex
	calls int
}

func (r *slowRunner) Definitions() []llm.Tool { return nil }

func (r *slowRunner) Run(ctx context.Context, name string, args json.RawMessage) (string, error) {
	r.mu.Lock()
	r.calls++
	r.mu.Unlock()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(100 * time.Millisecond):
		return string(args), nil
	}
}
