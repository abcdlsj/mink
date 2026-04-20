package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/llm"
	"github.com/abcdlsj/mink/msg"
	"github.com/abcdlsj/mink/session"
	"github.com/abcdlsj/mink/tool"
)

type Runtime interface {
	Run(context.Context, *Turn) error
}

type RuntimeFactory func(*RuntimeEnv) (Runtime, error)

type RuntimeEnv struct {
	Provider             llm.Provider
	Tools                *tool.Registry
	Workspace            string
	SoulPath             string
	Prompt               string
	TelegramMentionMode  string
	TelegramSessionScope string
	MaxSteps             int
}

type Turn struct {
	Source  string
	Input   string
	Session *session.Session
	Bus     *bus.Bus
}

type Native struct {
	env *RuntimeEnv
}

func NewNative(env *RuntimeEnv) (Runtime, error) {
	if env == nil || env.Provider == nil {
		return nil, fmt.Errorf("native runtime requires provider")
	}
	if env.Tools == nil {
		return nil, fmt.Errorf("native runtime requires tools")
	}
	if env.MaxSteps <= 0 {
		env.MaxSteps = 8
	}
	return &Native{env: env}, nil
}

func (n *Native) Run(ctx context.Context, t *Turn) error {
	t.Session.Add(msg.Message{
		ID:        uuid.New().String()[:8],
		Role:      "user",
		Content:   t.Input,
		Timestamp: now(),
	})

	for step := 0; step < n.env.MaxSteps; step++ {
		resp, err := n.runOnce(ctx, t)
		if err != nil {
			return err
		}
		if len(resp.ToolCalls) == 0 {
			return nil
		}
	}
	return fmt.Errorf("max steps reached")
}

func (n *Native) runOnce(ctx context.Context, t *Turn) (*llm.Response, error) {
	msgs := append([]msg.Message{{
		Role:    "system",
		Content: n.systemPrompt(t),
	}}, t.Session.Messages...)

	ch, err := n.env.Provider.ChatStream(ctx, msgs, n.env.Tools.Definitions())
	if err != nil {
		return nil, err
	}

	var text strings.Builder
	var reasoning strings.Builder
	var toolCalls []msg.ToolCall
	var usage *llm.TokenUsage

	for part := range ch {
		switch part.Type {
		case llm.ChunkText:
			text.WriteString(part.Delta)
			n.publish(t, bus.Event{
				Type:      bus.TurnChunk,
				Source:    t.Source,
				SessionID: t.Session.ID,
				Text:      part.Delta,
			})
		case llm.ChunkReasoning:
			reasoning.WriteString(part.ReasoningDelta)
		case llm.ChunkToolCall:
			if part.ToolCall != nil {
				toolCalls = append(toolCalls, *part.ToolCall)
			}
		case llm.ChunkDone:
			usage = part.Usage
		case llm.ChunkError:
			if part.Error != nil {
				return nil, part.Error
			}
		}
	}

	res := &llm.Response{
		Content:   text.String(),
		Reasoning: reasoning.String(),
		ToolCalls: toolCalls,
		Usage:     usage,
	}
	if len(toolCalls) == 0 {
		t.Session.Add(msg.Message{
			ID:        uuid.New().String()[:8],
			Role:      "assistant",
			Content:   res.Content,
			Reasoning: res.Reasoning,
			Timestamp: now(),
		})
		return res, nil
	}

	t.Session.Add(msg.Message{
		ID:        uuid.New().String()[:8],
		Role:      "assistant",
		Content:   res.Content,
		Reasoning: res.Reasoning,
		ToolCalls: res.ToolCalls,
		Timestamp: now(),
	})

	for _, tc := range toolCalls {
		n.publish(t, bus.Event{
			Type:       bus.ToolCallStarted,
			Source:     t.Source,
			SessionID:  t.Session.ID,
			ToolCallID: tc.ID,
			Tool:       tc.Name,
			Input:      string(tc.Args),
		})
		out, err := n.env.Tools.Run(ctx, tc.Name, tc.Args)
		result := msg.ToolResult{ToolCallID: tc.ID, Content: out}
		if err != nil {
			result.Error = err.Error()
		}
		t.Session.Add(msg.Message{
			ID:          uuid.New().String()[:8],
			Role:        "tool",
			ToolResults: []msg.ToolResult{result},
			Timestamp:   now(),
		})
		typ := bus.ToolCallFinished
		if result.Error != "" {
			typ = bus.ToolCallFailed
		}
		n.publish(t, bus.Event{
			Type:       typ,
			Source:     t.Source,
			SessionID:  t.Session.ID,
			ToolCallID: tc.ID,
			Tool:       tc.Name,
			Input:      string(tc.Args),
			Output:     out,
			Err:        result.Error,
		})
	}
	return res, nil
}

func (n *Native) systemPrompt(t *Turn) string {
	return BuildSystemPrompt(n.env, t)
}

func (n *Native) publish(t *Turn, ev bus.Event) {
	if t.Bus == nil {
		return
	}
	if ev.Source == "" {
		ev.Source = t.Source
	}
	if ev.SessionID == "" {
		ev.SessionID = t.Session.ID
	}
	t.Bus.Publish(ev)
}

func now() time.Time {
	return time.Now()
}
