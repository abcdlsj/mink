package external

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/abcdlsj/sumi/agent"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/session"
	"github.com/abcdlsj/sumi/textutil"
)

type Driver struct {
	Name          string
	Command       string
	StdinPrompt   bool
	BuildArgs     func(prompt, workDir, sessionID string, resume bool) []string
	ParseOutput   func(line string) *Message
	FormatHistory func(messages []msg.Message) string
	RuntimeMeta   func(context.Context) map[string]string
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
	sessionID, resume := r.externalSession(turn)
	prompt := textutil.Valid(r.buildPrompt(turn, !resume || turn.IncludeHistory))
	fallbackPrompt := textutil.Valid(r.buildPrompt(turn, true))
	addUser(turn.Session, turn.Input)

	st := newRunState()
	runErr := r.runCommand(ctx, turn, st, prompt, sessionID, resume)
	if runErr != nil && resume && missingExternalSession(runErr) {
		sessionID = r.resetSessionID(turn.Session)
		st = newRunState()
		runErr = r.runCommand(ctx, turn, st, fallbackPrompt, sessionID, false)
	}
	st.flush(turn.Session)
	return runErr
}

func (r *Runtime) externalSession(turn *agent.Turn) (string, bool) {
	if turn != nil && turn.DisableExternalResume {
		return "", false
	}
	if turn == nil {
		return "", false
	}
	return r.getOrCreateSessionID(turn.Session)
}

func newRunState() *runState {
	return &runState{calls: map[string]toolCallState{}}
}

func (r *Runtime) runCommand(ctx context.Context, turn *agent.Turn, st *runState, prompt, sessionID string, resume bool) error {
	if r.driver.RuntimeMeta != nil {
		if meta := r.driver.RuntimeMeta(ctx); len(meta) > 0 {
			st.onRuntimeMeta(turn, &Message{Type: MsgRuntimeMeta, Meta: meta})
		}
	}
	cmd := exec.CommandContext(ctx, r.driver.Command, r.driver.BuildArgs(prompt, r.workspace, sessionID, resume)...)
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
		if err := handleMessage(r.driver.Name, turn, st, m); err != nil {
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
	if runErr != nil && stderrText != "" {
		runErr = fmt.Errorf("%w: %s", runErr, stderrText)
	}
	if runErr == nil && waitErr != nil {
		if stderrText != "" {
			runErr = errors.New(stderrText)
		} else {
			runErr = waitErr
		}
	}
	return runErr
}

func (r *Runtime) buildPrompt(turn *agent.Turn, includeHistory bool) string {
	var hist string
	if includeHistory && turn != nil && turn.Session != nil {
		if r.driver.FormatHistory != nil {
			hist = r.driver.FormatHistory(turn.Session.Messages)
		} else {
			hist = FormatHistory(turn.Session.Messages)
		}
	}
	return agent.BuildExternalPrompt(r.env, turn, hist)
}

func (r *Runtime) getOrCreateSessionID(s *session.Session) (string, bool) {
	if s == nil {
		return "", false
	}
	if s.ExternalSession == nil {
		s.ExternalSession = map[string]string{}
	}
	key := r.sessionKey()
	if sid := s.ExternalSession[key]; sid != "" {
		return sid, true
	}
	sid := uuid.New().String()
	s.ExternalSession[key] = sid
	return sid, false
}

func (r *Runtime) resetSessionID(s *session.Session) string {
	sid := uuid.New().String()
	if s == nil {
		return sid
	}
	if s.ExternalSession == nil {
		s.ExternalSession = map[string]string{}
	}
	s.ExternalSession[r.sessionKey()] = sid
	return sid
}

func (r *Runtime) sessionKey() string {
	name := strings.TrimSpace(r.driver.Name)
	if name == "" {
		name = strings.TrimSpace(r.driver.Command)
	}
	if name == "" {
		name = "external"
	}
	workspace := strings.TrimSpace(r.workspace)
	if workspace == "" || workspace == "." {
		return name
	}
	return name + ":" + filepath.Clean(workspace)
}

func missingExternalSession(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "no conversation found with session id")
}

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
