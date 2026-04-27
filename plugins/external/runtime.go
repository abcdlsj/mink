package external

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/google/uuid"

	"github.com/abcdlsj/mink/agent"
	"github.com/abcdlsj/mink/msg"
	"github.com/abcdlsj/mink/session"
	"github.com/abcdlsj/mink/textutil"
)

type Driver struct {
	Name          string
	Command       string
	StdinPrompt   bool
	BuildArgs     func(prompt, workDir, sessionID string) []string
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
	Snapshot     bool
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
	prompt := textutil.Valid(r.buildPrompt(turn))
	addUser(turn.Session, turn.Input)

	sessionID := r.getOrCreateSessionID(turn.Session)
	cmd := exec.CommandContext(ctx, r.driver.Command, r.driver.BuildArgs(prompt, r.workspace, sessionID)...)
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
	defer st.flush(turn.Session)
	scanner := bufio.NewScanner(stdout)
	const maxLine = 10 * 1024 * 1024
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, maxLine)

	var runErr error
	for scanner.Scan() {
		m := r.driver.ParseOutput(scanner.Text())
		if m == nil {
			continue
		}
		if err := handleMessage(r.driver.Name, turn, &st, m); err != nil {
			runErr = err
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			break
		}
	}
	if err := scanner.Err(); err != nil && runErr == nil {
		runErr = err
	}
	waitErr := cmd.Wait()
	stderrText := <-errCh
	if runErr == nil && waitErr != nil {
		if stderrText != "" {
			runErr = errors.New(stderrText)
		} else {
			runErr = waitErr
		}
	}
	return runErr
}

func (r *Runtime) buildPrompt(turn *agent.Turn) string {
	return agent.BuildExternalPrompt(r.env, turn, "")
}

func (r *Runtime) getOrCreateSessionID(s *session.Session) string {
	if s == nil || s.ExternalSession == nil {
		return ""
	}
	if sid := s.ExternalSession[r.driver.Name]; sid != "" {
		return sid
	}
	sid := uuid.New().String()
	s.ExternalSession[r.driver.Name] = sid
	return sid
}

func handleMessage(name string, turn *agent.Turn, st *runState, m *Message) error {
	switch m.Type {
	case MsgStreamChunk:
		st.onStream(turn, m.Text)
	case MsgAssistantText:
		st.onAssistant(turn, m.Text, m.Snapshot)
	case MsgThinkingChunk:
		st.reasoning.WriteString(m.Text)
	case MsgToolCall:
		st.onToolCall(turn, m)
	case MsgToolResult:
		st.onToolResult(turn, m)
	case MsgError:
		return wrapMessageError(name, m)
	}
	return nil
}
