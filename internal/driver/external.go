package driver

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type CommandRunner interface {
	Run(context.Context, []byte, func([]byte) error) error
}

type External struct {
	Kind   Kind
	Runner CommandRunner
}

func (d External) Execute(ctx context.Context, command Command, events EventSink) (TurnResult, error) {
	if err := command.validate(); err != nil {
		return TurnResult{}, err
	}
	if !d.Kind.Valid() || d.Kind == KindNative {
		return TurnResult{}, errors.New("external driver kind is invalid")
	}
	if d.Runner == nil {
		return TurnResult{}, errors.New("external driver command runner is required")
	}
	prompt, err := promptForCommand(command)
	if err != nil {
		return TurnResult{}, err
	}
	payload, err := json.Marshal(wireCommand{Kind: command.Kind, Prompt: prompt, Text: command.Text, ChildName: command.ChildName})
	if err != nil {
		return TurnResult{}, fmt.Errorf("encode driver command: %w", err)
	}
	var result *TurnResult
	err = d.Runner.Run(ctx, payload, func(line []byte) error {
		message, err := parseWireLine(line)
		if err != nil {
			return err
		}
		if message.Type == "event" {
			if events == nil {
				return nil
			}
			return events.Publish(Event{Kind: message.Kind, Text: message.Text, Tool: message.Tool, Call: message.Call})
		}
		if message.Result != nil {
			if result != nil {
				return errors.New("external driver returned multiple results")
			}
			result = message.Result
		}
		return nil
	})
	if err != nil {
		return TurnResult{}, err
	}
	if result == nil {
		return TurnResult{}, errors.New("external driver returned no result")
	}
	if err := result.validate(); err != nil {
		return TurnResult{}, err
	}
	return *result, nil
}

func promptForCommand(command Command) (*Prompt, error) {
	if command.Kind != CommandPrompt {
		return nil, nil
	}
	if command.Input == nil {
		return nil, errors.New("prompt input is required")
	}
	prompt, err := command.Input.Prompt()
	if err != nil {
		return nil, err
	}
	return &prompt, nil
}

type wireCommand struct {
	Kind      CommandKind `json:"kind"`
	Prompt    *Prompt     `json:"prompt,omitempty"`
	Text      string      `json:"text,omitempty"`
	ChildName string      `json:"child_name,omitempty"`
}

type wireLine struct {
	Type   string      `json:"type"`
	Kind   EventKind   `json:"kind,omitempty"`
	Text   string      `json:"text,omitempty"`
	Tool   string      `json:"tool,omitempty"`
	Call   *ModelCall  `json:"call,omitempty"`
	Result *TurnResult `json:"result,omitempty"`
}

func parseWireLine(line []byte) (wireLine, error) {
	var message wireLine
	if err := json.Unmarshal(line, &message); err != nil {
		return wireLine{}, fmt.Errorf("decode external driver event: %w", err)
	}
	switch message.Type {
	case "event":
		if message.Kind == "" {
			return wireLine{}, errors.New("external driver event kind is required")
		}
		return message, nil
	case "result":
		if message.Result == nil {
			return wireLine{}, errors.New("external driver result is required")
		}
		return message, nil
	default:
		return wireLine{}, fmt.Errorf("unsupported external driver message type %q", message.Type)
	}
}

type JSONLRunner struct {
	Command func(context.Context, []byte) (string, error)
}

func (r JSONLRunner) Run(ctx context.Context, input []byte, emit func([]byte) error) error {
	if r.Command == nil {
		return errors.New("jsonl command is required")
	}
	output, err := r.Command(ctx, input)
	if err != nil {
		return err
	}
	scanner := bufio.NewScanner(strings.NewReader(output))
	scanner.Buffer(make([]byte, 64*1024), 512*1024)
	for scanner.Scan() {
		if err := emit(scanner.Bytes()); err != nil {
			return err
		}
	}
	return scanner.Err()
}
