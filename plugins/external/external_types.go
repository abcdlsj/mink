package external

import (
	"context"

	"github.com/abcdlsj/sumi/msg"
)

type Driver struct {
	Name                 string
	Command              string
	StdinPrompt          bool
	BuildArgs            func(prompt, workDir, sessionID string, resume bool) []string
	BuildArgsWithProfile func(prompt, workDir, sessionID string, resume bool, profile Profile) []string
	ParseOutput          func(line string) *Message
	FormatHistory        func(messages []msg.Message) string
	RuntimeMeta          func(context.Context) map[string]string
}

type Profile struct {
	Isolated     bool
	Runtime      string
	Root         string
	Home         string
	CodexHome    string
	SettingsPath string
	PluginDirs   []string
	Env          []string
}

type MessageType int

const (
	MsgAssistantText MessageType = iota
	MsgStreamChunk
	MsgThinkingChunk
	MsgToolCall
	MsgToolResult
	MsgTurnDone
	MsgRuntimeMeta
	MsgError
)

type Message struct {
	Type     MessageType
	Text     string
	Snapshot bool
	ToolName string
	ToolArgs string
	ToolID   string
	Stderr   string
	ExitCode int
	IsError  bool
	Usage    *msg.TokenUsage
	Model    string
	CostUSD  float64
	Reason   string
	Meta     map[string]string
	Error    error
}
