package agent

import (
	"context"
	"time"

	"github.com/abcdlsj/mink/msg"
	"github.com/abcdlsj/mink/session"
)

type Runtime interface {
	Start(ctx context.Context, cfg RuntimeConfig) error

	Send(ctx context.Context, input string) error

	SendSystem(ctx context.Context, input string) error

	Stop() error

	Status() RuntimeStatus

	Session() *session.Session

	TokenUsage() msg.TokenUsage

	Interrupt()
}

type RuntimeConfig struct {
	Source   string
	AgentID  string
	Session  *session.Session
	SubAgent bool
}

type RuntimeStatus int

const (
	RuntimeIdle RuntimeStatus = iota
	RuntimeRunning
	RuntimeStopped
)

func (s RuntimeStatus) String() string {
	switch s {
	case RuntimeIdle:
		return "idle"
	case RuntimeRunning:
		return "running"
	case RuntimeStopped:
		return "stopped"
	default:
		return "unknown"
	}
}

type RuntimeMessage struct {
	Type      RuntimeMessageType
	Text      string
	ToolName  string
	ToolArgs  string
	ToolID    string
	SessionID string
	Error     error
	Timestamp time.Time
}

type RuntimeMessageType int

const (
	MsgAssistantText RuntimeMessageType = iota
	MsgStreamChunk
	MsgStreamEnd
	MsgToolCall
	MsgToolResult
	MsgToolError
	MsgThinkingChunk
	MsgThinkingEnd
	MsgTurnDone
	MsgError
)
