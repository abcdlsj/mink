package external

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/abcdlsj/mink/agent"
	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/msg"
	"github.com/abcdlsj/mink/session"
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
			workspace: env.Workspace,
		}, nil
	}
}

type Runtime struct {
	driver    Driver
	workspace string
}

func (r *Runtime) Run(ctx context.Context, turn *agent.Turn) error {
	prompt := turn.Input
	if r.driver.FormatHistory != nil {
		msgs := turn.Session.Messages
		if len(msgs) > 0 {
			if h := strings.TrimSpace(r.driver.FormatHistory(msgs)); h != "" {
				prompt = h + "\n\n" + turn.Input
			}
		}
	}
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

	var assistant strings.Builder
	var reasoning strings.Builder
	var order []string
	toolCalls := map[string]msg.ToolCall{}
	toolOut := map[string]string{}
	scanner := bufio.NewScanner(stdout)
	const maxLine = 10 * 1024 * 1024
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, maxLine)

	for scanner.Scan() {
		m := r.driver.ParseOutput(scanner.Text())
		if m == nil {
			continue
		}
		switch m.Type {
		case MsgAssistantText, MsgStreamChunk:
			assistant.WriteString(m.Text)
			publish(turn, bus.Event{
				Type:      bus.TurnChunk,
				Source:    turn.Source,
				SessionID: turn.Session.ID,
				Text:      m.Text,
			})
		case MsgThinkingChunk:
			reasoning.WriteString(m.Text)
		case MsgToolCall:
			if m.ToolID == "" {
				m.ToolID = uuid.New().String()[:8]
			}
			if _, ok := toolCalls[m.ToolID]; !ok {
				order = append(order, m.ToolID)
			}
			toolCalls[m.ToolID] = msg.ToolCall{
				ID:   m.ToolID,
				Name: m.ToolName,
				Args: json.RawMessage(m.ToolArgs),
			}
			publish(turn, bus.Event{
				Type:       bus.ToolCallStarted,
				Source:     turn.Source,
				SessionID:  turn.Session.ID,
				ToolCallID: m.ToolID,
				Tool:       m.ToolName,
				Input:      m.ToolArgs,
			})
		case MsgToolResult:
			toolOut[m.ToolID] = m.Text
			tc := toolCalls[m.ToolID]
			publish(turn, bus.Event{
				Type:       bus.ToolCallFinished,
				Source:     turn.Source,
				SessionID:  turn.Session.ID,
				ToolCallID: m.ToolID,
				Tool:       tc.Name,
				Input:      string(tc.Args),
				Output:     m.Text,
			})
		case MsgError:
			err := m.Error
			if err == nil && m.Text != "" {
				err = errors.New(m.Text)
			}
			if err == nil {
				err = fmt.Errorf("%s runtime failed", r.driver.Name)
			}
			publish(turn, bus.Event{
				Type:      bus.TurnError,
				Source:    turn.Source,
				SessionID: turn.Session.ID,
				Err:       err.Error(),
			})
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
	addAssistant(turn.Session, assistant.String(), reasoning.String(), toolCalls, order)
	addToolResults(turn.Session, toolCalls, toolOut, order)
	return nil
}

func addUser(s *session.Session, input string) {
	if s == nil || strings.TrimSpace(input) == "" {
		return
	}
	s.Add(msg.Message{
		ID:        uuid.New().String()[:8],
		Role:      "user",
		Content:   input,
		Timestamp: time.Now(),
	})
}

func addAssistant(s *session.Session, text, reasoning string, calls map[string]msg.ToolCall, order []string) {
	if strings.TrimSpace(text) == "" && len(calls) == 0 {
		return
	}
	var list []msg.ToolCall
	if len(calls) > 0 {
		list = make([]msg.ToolCall, 0, len(calls))
		for _, id := range stableIDs(order, calls) {
			list = append(list, calls[id])
		}
	}
	s.Add(msg.Message{
		ID:        uuid.New().String()[:8],
		Role:      "assistant",
		Content:   text,
		Reasoning: reasoning,
		ToolCalls: list,
		Timestamp: time.Now(),
	})
}

func addToolResults(s *session.Session, calls map[string]msg.ToolCall, outs map[string]string, order []string) {
	if len(outs) == 0 {
		return
	}
	results := make([]msg.ToolResult, 0, len(outs))
	for _, id := range stableIDs(order, calls) {
		out, ok := outs[id]
		if !ok {
			continue
		}
		results = append(results, msg.ToolResult{
			ToolCallID: id,
			Content:    out,
		})
	}
	if len(results) == 0 {
		return
	}
	s.Add(msg.Message{
		ID:          uuid.New().String()[:8],
		Role:        "tool",
		ToolResults: results,
		Timestamp:   time.Now(),
	})
}

func stableIDs(order []string, calls map[string]msg.ToolCall) []string {
	if len(order) != 0 {
		out := make([]string, 0, len(order))
		for _, id := range order {
			if _, ok := calls[id]; ok {
				out = append(out, id)
			}
		}
		return out
	}
	out := make([]string, 0, len(calls))
	for id := range calls {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
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
