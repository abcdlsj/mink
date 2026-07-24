package codex

import "testing"

func TestCodexJSONLResult(t *testing.T) {
	var result codexResult
	for _, line := range []string{
		`{"type":"thread.started","thread_id":"thread-1"}`,
		`{"type":"item.completed","item":{"type":"agent_message","text":"hello "}}`,
		`{"type":"item.completed","item":{"type":"command_execution","text":"must not leak"}}`,
		`{"type":"item.completed","item":{"type":"agent_message","text":"world"}}`,
		`{"type":"turn.completed","usage":{"input_tokens":7,"output_tokens":3}}`,
	} {
		if err := result.consume([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	completion, err := result.completion()
	if err != nil {
		t.Fatal(err)
	}
	if completion.Body != "hello world" || completion.Usage.InputUnits != 7 || completion.Usage.OutputUnits != 3 {
		t.Fatalf("completion = %+v", completion)
	}
}

func TestCodexJSONLRejectsProtocolFailure(t *testing.T) {
	for name, line := range map[string]string{
		"malformed": `{`,
		"error":     `{"type":"error","message":"provider secret must not surface"}`,
		"failed":    `{"type":"turn.failed","error":{"message":"failure detail"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			var result codexResult
			if err := result.consume([]byte(line)); err == nil {
				t.Fatal("consume accepted protocol failure")
			}
		})
	}
	if _, err := (codexResult{}).completion(); err == nil {
		t.Fatal("completion accepted a missing final message")
	}
}
