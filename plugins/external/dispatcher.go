package external

import (
	"errors"
	"fmt"

	"github.com/abcdlsj/sumi/agent"
)

func handleMessage(name string, turn *agent.Turn, st *runState, m *Message) error {
	switch m.Type {
	case MsgStreamChunk:
		st.onStream(turn, m.Text)
	case MsgAssistantText:
		st.onAssistant(turn, m.Text, m.Snapshot)
	case MsgThinkingChunk:
		st.onThinking(turn, m.Text)
	case MsgToolCall:
		st.onToolCall(turn, m)
	case MsgToolResult:
		st.onToolResult(turn, m)
	case MsgTurnDone:
		st.onTurnDone(turn, m)
	case MsgRuntimeMeta:
		st.onRuntimeMeta(turn, m)
	case MsgError:
		return wrapMessageError(name, m)
	}
	return nil
}

func wrapMessageError(name string, m *Message) error {
	err := m.Error
	if err == nil && m != nil && m.Text != "" {
		err = errors.New(m.Text)
	}
	if err == nil {
		err = fmt.Errorf("%s runtime failed", name)
	}
	return err
}
