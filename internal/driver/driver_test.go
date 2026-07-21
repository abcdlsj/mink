package driver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPromptUsesStableSectionOrderAndNoSecretField(t *testing.T) {
	input := testInput()
	input.MemoryIndex = []string{"notes/runtime.md#turn"}
	input.Sources = []Source{{ID: "message-1", Kind: "message", Text: "source text"}}
	prompt, err := input.Prompt()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"host_policy", "agent_identity", "placement", "capabilities", "target_context", "memory_index", "retrieved_source", "current_input"}
	if len(prompt.Sections) != len(want) {
		t.Fatalf("section count = %d, want %d", len(prompt.Sections), len(want))
	}
	for index, section := range prompt.Sections {
		if section.Name != want[index] {
			t.Fatalf("section %d = %q, want %q", index, section.Name, want[index])
		}
	}
	payload, err := json.Marshal(prompt)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "secret") {
		t.Fatalf("prompt contains forbidden secret field: %s", payload)
	}
}

func TestOwnerSerializesCommandsAndNumbersEvents(t *testing.T) {
	var mu sync.Mutex
	var commands []CommandKind
	engine := Native{ExecuteFunc: func(ctx context.Context, command Command, events EventSink) (TurnResult, error) {
		mu.Lock()
		commands = append(commands, command.Kind)
		mu.Unlock()
		if err := events.Publish(Event{Kind: EventStarted}); err != nil {
			return TurnResult{}, err
		}
		return TurnResult{Outcome: OutcomeSucceeded, Body: command.Text}, nil
	}}
	owner, err := NewOwner(engine, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	first, err := owner.Submit(context.Background(), Command{Kind: CommandSteer, Text: "first"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := owner.Submit(context.Background(), Command{Kind: CommandSteer, Text: "second"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Body != "first" || second.Body != "second" {
		t.Fatalf("results = %#v, %#v", first, second)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	var events []Event
	for event := range owner.Events() {
		events = append(events, event)
	}
	if len(events) != 2 || events[0].Sequence != 1 || events[1].Sequence != 2 {
		t.Fatalf("events = %#v", events)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(stringKinds(commands), ",") != "steer,steer" {
		t.Fatalf("commands = %#v", commands)
	}
}

func TestOwnerRejectsCommandsAboveBoundedQueue(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	engine := Native{ExecuteFunc: func(ctx context.Context, command Command, events EventSink) (TurnResult, error) {
		close(started)
		select {
		case <-release:
			return TurnResult{Outcome: OutcomeSucceeded}, nil
		case <-ctx.Done():
			return TurnResult{}, ctx.Err()
		}
	}}
	owner, err := NewOwner(engine, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	firstDone := make(chan struct{})
	go func() {
		_, _ = owner.Submit(context.Background(), Command{Kind: CommandSteer, Text: "first"})
		close(firstDone)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("engine did not start")
	}
	if _, err := owner.Submit(context.Background(), Command{Kind: CommandSteer, Text: "queued"}); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Submit(context.Background(), Command{Kind: CommandSteer, Text: "rejected"}); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("third submit error = %v, want ErrQueueFull", err)
	}
	close(release)
	<-firstDone
}

func TestOwnerDoesNotDependOnEventConsumerForResult(t *testing.T) {
	engine := Native{ExecuteFunc: func(_ context.Context, _ Command, events EventSink) (TurnResult, error) {
		if err := events.Publish(Event{Kind: EventStarted}); err != nil {
			return TurnResult{}, err
		}
		if err := events.Publish(Event{Kind: EventDelta, Text: "discardable"}); err != nil {
			return TurnResult{}, err
		}
		return TurnResult{Outcome: OutcomeSucceeded, Body: "done"}, nil
	}}
	owner, err := NewOwner(engine, 1)
	if err != nil {
		t.Fatal(err)
	}
	result, err := owner.Submit(context.Background(), Command{Kind: CommandSteer, Text: "run"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Body != "done" {
		t.Fatalf("body = %q", result.Body)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestExternalNormalizesJSONLEventsAndResult(t *testing.T) {
	var received []byte
	runner := JSONLRunner{Command: func(_ context.Context, input []byte) (string, error) {
		received = append([]byte(nil), input...)
		return "{\"type\":\"event\",\"kind\":\"started\"}\n{\"type\":\"result\",\"result\":{\"outcome\":\"succeeded\",\"body\":\"done\"}}\n", nil
	}}
	driver := External{Kind: KindCodex, Runner: runner}
	var events []Event
	input := testInput()
	result, err := driver.Execute(context.Background(), Command{Kind: CommandPrompt, Input: &input}, eventCollector{events: &events})
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != OutcomeSucceeded || result.Body != "done" || len(events) != 1 || events[0].Kind != EventStarted {
		t.Fatalf("result/events = %#v/%#v", result, events)
	}
	if strings.Contains(string(received), "secret") {
		t.Fatalf("external payload contains forbidden secret field: %s", received)
	}
}

func TestExternalRejectsMalformedJSONL(t *testing.T) {
	driver := External{Kind: KindClaude, Runner: JSONLRunner{Command: func(context.Context, []byte) (string, error) {
		return "{\"type\":\"unknown\"}\n", nil
	}}}
	input := testInput()
	_, err := driver.Execute(context.Background(), Command{Kind: CommandPrompt, Input: &input}, eventCollector{})
	if err == nil || !strings.Contains(err.Error(), "unsupported external driver message type") {
		t.Fatalf("error = %v", err)
	}
}

type eventCollector struct {
	events *[]Event
}

func (c eventCollector) Publish(event Event) error {
	if c.events != nil {
		*c.events = append(*c.events, event)
	}
	return nil
}

func testInput() RunInput {
	return RunInput{
		AgentID:      "agent-1",
		ComputerID:   "computer-1",
		Generation:   1,
		DeliveryID:   "delivery-1",
		RunID:        "run-1",
		LaunchID:     "launch-1",
		Fence:        1,
		Workspace:    "/tmp/agent-1",
		Capabilities: Capability{Streaming: true, Tools: true, Cancel: true},
		Target:       Target{SpaceID: "space-1", HeadSequence: 2},
		CurrentInput: "finish the task",
		HostPolicy:   "follow the host contract",
	}
}

func stringKinds(values []CommandKind) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = string(value)
	}
	return result
}
