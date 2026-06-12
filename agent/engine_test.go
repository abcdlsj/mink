package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/abcdlsj/sumi/llm"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/session"
)

func TestEngineDowngradesUnsupportedImageInput(t *testing.T) {
	p := &imageRejectingProvider{}
	e := &engine{env: &RuntimeEnv{
		Provider: p,
		Tools:    noopTools{},
	}}
	s := session.New("tg:dm:1")
	turn := &Turn{
		Input: "请看这张图片。\n\nAttached images:\n[image_1: photo.jpg]",
		Attachments: []msg.Attachment{{
			Kind: "image",
			Name: "photo.jpg",
			MIME: "image/jpeg",
			Data: "abcd",
		}},
		Session: s,
	}

	if err := e.run(context.Background(), turn); err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if p.calls != 2 {
		t.Fatalf("calls = %d, want 2", p.calls)
	}
	if len(s.Messages) != 2 {
		t.Fatalf("messages = %d, want user + assistant", len(s.Messages))
	}
	if len(s.Messages[0].Attachments) != 0 {
		t.Fatalf("attachments were not downgraded: %#v", s.Messages[0].Attachments)
	}
	if s.Messages[1].Content != "我现在不能直接查看图片，请描述一下图片内容。" {
		t.Fatalf("assistant content = %q", s.Messages[1].Content)
	}
}

func TestEngineFiltersBlockedToolDefinitions(t *testing.T) {
	p := &toolCapturingProvider{}
	e := &engine{env: &RuntimeEnv{
		Provider: p,
		Tools: filteringTools{defs: []llm.Tool{
			functionTool("read"),
			functionTool("task_create"),
			functionTool("task_assign"),
		}},
	}}
	if err := e.run(context.Background(), &Turn{
		Input:   "解释一下这个链接是什么",
		Session: session.New("desktop:channel:alpha"),
		BlockedTools: map[string]string{
			"task_create": "no explicit task intent",
			"task_assign": "no explicit task intent",
		},
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(p.toolNames, ",") != "read" {
		t.Fatalf("tools = %#v, want only read", p.toolNames)
	}
}

func TestToolExecutorRejectsBlockedToolCalls(t *testing.T) {
	runner := &recordingToolRunner{}
	turn := &Turn{
		Session:      session.New("cli"),
		BlockedTools: map[string]string{"task_create": "no explicit task intent"},
	}
	toolExecutor{tools: runner}.run(context.Background(), turn, []msg.ToolCall{{
		ID:   "call-1",
		Name: "task_create",
		Args: json.RawMessage(`{}`),
	}})
	if runner.calls != 0 {
		t.Fatalf("blocked tool reached runner")
	}
	if len(turn.Session.Messages) != 1 || !strings.Contains(turn.Session.Messages[0].ToolResults[0].Error, "not available in this turn") {
		t.Fatalf("tool result = %#v", turn.Session.Messages)
	}
}

type imageRejectingProvider struct {
	calls int
}

func (p *imageRejectingProvider) Chat(context.Context, []msg.Message, []llm.Tool) (*llm.Response, error) {
	return nil, fmt.Errorf("not used")
}

func (p *imageRejectingProvider) ChatStream(_ context.Context, msgs []msg.Message, _ []llm.Tool) (<-chan llm.Chunk, error) {
	p.calls++
	if p.calls == 1 {
		for _, m := range msgs {
			for _, a := range m.Attachments {
				if a.Kind == "image" {
					return nil, fmt.Errorf("unknown variant image_url, expected text")
				}
			}
		}
		t := make(chan llm.Chunk)
		close(t)
		return t, nil
	}
	for _, m := range msgs {
		if len(m.Attachments) > 0 {
			return nil, fmt.Errorf("attachments still present on retry")
		}
	}
	ch := make(chan llm.Chunk, 2)
	ch <- llm.Chunk{Type: llm.ChunkText, Delta: "我现在不能直接查看图片，请描述一下图片内容。"}
	ch <- llm.Chunk{Type: llm.ChunkDone}
	close(ch)
	return ch, nil
}

type noopTools struct{}

func (noopTools) Definitions() []llm.Tool { return nil }

func (noopTools) Run(context.Context, string, json.RawMessage) (string, error) {
	return "", nil
}

type toolCapturingProvider struct {
	toolNames []string
}

func (p *toolCapturingProvider) Chat(context.Context, []msg.Message, []llm.Tool) (*llm.Response, error) {
	return nil, fmt.Errorf("not used")
}

func (p *toolCapturingProvider) ChatStream(_ context.Context, _ []msg.Message, tools []llm.Tool) (<-chan llm.Chunk, error) {
	for _, tool := range tools {
		if tool.Function != nil {
			p.toolNames = append(p.toolNames, tool.Function.Name)
		}
	}
	ch := make(chan llm.Chunk, 2)
	ch <- llm.Chunk{Type: llm.ChunkText, Delta: "ok"}
	ch <- llm.Chunk{Type: llm.ChunkDone}
	close(ch)
	return ch, nil
}

type filteringTools struct {
	defs []llm.Tool
}

func (t filteringTools) Definitions() []llm.Tool {
	return append([]llm.Tool(nil), t.defs...)
}

func (filteringTools) Run(context.Context, string, json.RawMessage) (string, error) {
	return "ok", nil
}

type recordingToolRunner struct {
	calls int
}

func (r *recordingToolRunner) Definitions() []llm.Tool { return nil }

func (r *recordingToolRunner) Run(context.Context, string, json.RawMessage) (string, error) {
	r.calls++
	return "ok", nil
}

func functionTool(name string) llm.Tool {
	return llm.Tool{Type: "function", Function: &llm.FunctionDef{Name: name}}
}
