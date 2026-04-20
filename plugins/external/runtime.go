package external

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/abcdlsj/mink/agent"
	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/msg"
)

type Driver struct {
	Name          string
	Command       string
	StdinPrompt   bool
	BuildArgs     func(prompt, workDir string) []string
	ParseOutput   func(line string) *Message
	FormatHistory func(messages []msg.Message) string
}

type MessageType int

const (
	MsgAssistantText MessageType = iota
	MsgStreamChunk
	MsgThinkingChunk
	MsgToolCall
	MsgToolResult
	MsgTurnDone
	MsgError
)

type Message struct {
	Type         MessageType
	Text         string
	ToolName     string
	ToolArgs     string
	ToolID       string
	InputTokens  int
	OutputTokens int
	Error        error
}

func NewRuntime(driver Driver) agent.RuntimeFactory {
	return func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		if strings.TrimSpace(driver.Command) == "" {
			return nil, fmt.Errorf("external runtime command is empty")
		}
		return &Runtime{
			driver:    driver,
			env:       env,
			workspace: env.Workspace,
		}, nil
	}
}

type Runtime struct {
	driver    Driver
	env       *agent.RuntimeEnv
	workspace string
}

func (r *Runtime) Run(ctx context.Context, turn *agent.Turn) error {
	prompt := r.buildPrompt(turn)
	addUser(turn.Session, turn.Input)

	cmd := exec.CommandContext(ctx, r.driver.Command, r.driver.BuildArgs(prompt, r.workspace)...)
	if r.workspace != "" {
		cmd.Dir = r.workspace
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if r.driver.StdinPrompt {
		in, err := cmd.StdinPipe()
		if err != nil {
			return err
		}
		go func() {
			_, _ = io.WriteString(in, prompt)
			in.Close()
		}()
	}
	if err := cmd.Start(); err != nil {
		return err
	}

	errCh := make(chan string, 1)
	go func() {
		data, _ := io.ReadAll(stderr)
		errCh <- strings.TrimSpace(string(data))
	}()

	st := runState{
		calls: map[string]toolCallState{},
	}
	scanner := bufio.NewScanner(stdout)
	const maxLine = 10 * 1024 * 1024
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, maxLine)

	for scanner.Scan() {
		m := r.driver.ParseOutput(scanner.Text())
		if m == nil {
			continue
		}
		if err := handleMessage(r.driver.Name, turn, &st, m); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if err := cmd.Wait(); err != nil {
		stderrText := <-errCh
		if stderrText != "" {
			return errors.New(stderrText)
		}
		return err
	}
	<-errCh
	st.flush(turn.Session)
	return nil
}

func (r *Runtime) buildPrompt(turn *agent.Turn) string {
	var hist string
	if r.driver.FormatHistory != nil && turn != nil && turn.Session != nil {
		if msgs := turn.Session.Messages; len(msgs) > 0 {
			hist = strings.TrimSpace(r.driver.FormatHistory(msgs))
		}
	}
	return agent.BuildExternalPrompt(r.env, turn, hist)
}

func handleMessage(name string, turn *agent.Turn, st *runState, m *Message) error {
	switch m.Type {
	case MsgStreamChunk:
		st.onStream(turn, m.Text)
	case MsgAssistantText:
		st.onAssistant(turn, m.Text)
	case MsgThinkingChunk:
		st.reasoning.WriteString(m.Text)
	case MsgToolCall:
		st.onToolCall(turn, m)
	case MsgToolResult:
		st.onToolResult(turn, m)
	case MsgError:
		err := wrapMessageError(name, m)
		publish(turn, bus.Event{Type: bus.TurnError, Err: err.Error()})
		return err
	}
	return nil
}

func publish(turn *agent.Turn, ev bus.Event) {
	if turn == nil || turn.Bus == nil {
		return
	}
	if ev.Source == "" {
		ev.Source = turn.Source
	}
	if ev.SessionID == "" && turn.Session != nil {
		ev.SessionID = turn.Session.ID
	}
	turn.Bus.Publish(ev)
}
