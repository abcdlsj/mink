package external

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/abcdlsj/sumi/agent"
	"github.com/abcdlsj/sumi/textutil"
)

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
	profile, err := r.prepareProfile()
	if err != nil {
		return err
	}
	sessionID, resume := "", false
	if !profile.Isolated {
		sessionID, resume = r.externalSession(turn)
	}
	prompt := textutil.Valid(r.buildPrompt(turn, !resume || turn.IncludeHistory))
	fallbackPrompt := textutil.Valid(r.buildPrompt(turn, true))
	addUser(turn.Session, turn.Input, turn.Attachments)

	st := newRunState()
	runErr := r.runCommand(ctx, turn, st, prompt, sessionID, resume, profile)
	if runErr != nil && resume && !profile.Isolated && missingExternalSession(runErr) {
		sessionID = r.resetSessionID(turn.Session)
		st = newRunState()
		runErr = r.runCommand(ctx, turn, st, fallbackPrompt, sessionID, false, profile)
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

func (r *Runtime) runCommand(ctx context.Context, turn *agent.Turn, st *runState, prompt, sessionID string, resume bool, profile Profile) error {
	if r.driver.RuntimeMeta != nil {
		if meta := r.driver.RuntimeMeta(ctx); len(meta) > 0 {
			st.onRuntimeMeta(turn, &Message{Type: MsgRuntimeMeta, Meta: meta})
		}
	}
	cmd := exec.CommandContext(ctx, r.driver.Command, r.buildArgs(prompt, sessionID, resume, profile)...)
	cmd.Env = profile.Env
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
		return r.startError(err)
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
	if ctxErr := ctx.Err(); ctxErr != nil {
		return r.contextError(ctxErr)
	}
	if runErr != nil && stderrText != "" {
		runErr = fmt.Errorf("%w: %s", runErr, summarizeStderr(stderrText))
	}
	if runErr == nil && waitErr != nil {
		if stderrText != "" {
			runErr = r.exitError(errors.New(summarizeStderr(stderrText)))
		} else {
			runErr = r.exitError(waitErr)
		}
	}
	return runErr
}

func (r *Runtime) buildArgs(prompt, sessionID string, resume bool, profile Profile) []string {
	if r.driver.BuildArgsWithProfile != nil {
		return r.driver.BuildArgsWithProfile(prompt, r.workspace, sessionID, resume, profile)
	}
	if r.driver.BuildArgs != nil {
		return r.driver.BuildArgs(prompt, r.workspace, sessionID, resume)
	}
	return nil
}

func (r *Runtime) runtimeLabel() string {
	name := strings.TrimSpace(r.driver.Name)
	if name == "" {
		name = strings.TrimSpace(r.driver.Command)
	}
	if name == "" {
		return "external runtime"
	}
	return name
}

func (r *Runtime) startError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s unavailable: %w", r.runtimeLabel(), err)
}

func (r *Runtime) contextError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%s timed out: %w", r.runtimeLabel(), err)
	}
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("%s canceled: %w", r.runtimeLabel(), err)
	}
	return fmt.Errorf("%s stopped: %w", r.runtimeLabel(), err)
}

func (r *Runtime) exitError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s exited: %w", r.runtimeLabel(), err)
}

func summarizeStderr(stderr string) string {
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		if strings.Contains(lower, "failed to connect to websocket") {
			if strings.Contains(lower, "connection reset by peer") {
				return "failed to connect to websocket: connection reset by peer"
			}
			return "failed to connect to websocket"
		}
		if idx := strings.Index(line, " ERROR "); idx >= 0 {
			line = strings.TrimSpace(line[idx+len(" ERROR "):])
		}
		return trimErrorLine(line, 240)
	}
	return "runtime exited without stderr"
}

func trimErrorLine(line string, limit int) string {
	line = strings.Join(strings.Fields(strings.TrimSpace(line)), " ")
	if limit <= 0 || len([]rune(line)) <= limit {
		return line
	}
	rs := []rune(line)
	return string(rs[:limit]) + "..."
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
	if turn == nil {
		return agent.BuildExternalPrompt(r.env, turn, hist)
	}
	copyTurn := *turn
	copyTurn.Input = agent.UserInputWithAttachments(turn.Input, turn.Attachments)
	return agent.BuildExternalPrompt(r.env, &copyTurn, hist)
}

func missingExternalSession(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "no conversation found with session id")
}
