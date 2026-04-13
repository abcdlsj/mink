package agent

import (
	"context"
	"time"

	"github.com/abcdlsj/mink/msg"
	"github.com/abcdlsj/mink/session"
)

// Runtime abstracts agent execution so that both native (built-in ReAct loop)
// and external (Claude Code, Codex, etc.) runtimes can be managed through a
// single interface. See docs/rfcs/p6-runtime-abstraction.md.
type Runtime interface {
	// Start initializes the runtime with the given configuration.
	// Must be called before Send.
	Start(ctx context.Context, cfg RuntimeConfig) error

	// Send delivers user input to the runtime and blocks until the turn
	// completes. Output events are published to the bus during execution.
	Send(ctx context.Context, input string) error

	// SendSystem delivers a system-level input (e.g. team prompts).
	SendSystem(ctx context.Context, input string) error

	// Stop shuts down the runtime, releasing resources.
	Stop() error

	// Status returns the current runtime status.
	Status() RuntimeStatus

	// Session returns the underlying session for message history access.
	Session() *session.Session

	// TokenUsage returns accumulated token usage for the current session.
	TokenUsage() msg.TokenUsage

	// Interrupt signals the runtime to stop the current turn.
	Interrupt()
}

// RuntimeConfig carries initialization parameters for a Runtime.
type RuntimeConfig struct {
	Source    string
	AgentID  string
	Session  *session.Session
	SubAgent bool
}

// RuntimeStatus represents the current state of a runtime.
type RuntimeStatus int

const (
	RuntimeIdle    RuntimeStatus = iota
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

// RuntimeMessage is a tagged union for events emitted by a runtime.
// Used by external runtimes that communicate via channels rather than bus.
type RuntimeMessage struct {
	Type      RuntimeMessageType
	Text      string
	ToolName  string
	ToolArgs  string
	ToolID    string
	Error     error
	Timestamp time.Time
}

// RuntimeMessageType identifies the kind of runtime event.
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
