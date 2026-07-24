package claude

import "testing"

func TestClaudeJSONLResult(t *testing.T) {
	var result claudeResult
	for _, line := range []string{
		`{"type":"system","subtype":"init","session_id":"session-1"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"intermediate"}]}}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"final answer","usage":{"input_tokens":11,"output_tokens":5}}`,
	} {
		if err := result.consume([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	completion, err := result.completion()
	if err != nil {
		t.Fatal(err)
	}
	if completion.Body != "final answer" || completion.Usage.InputUnits != 11 || completion.Usage.OutputUnits != 5 {
		t.Fatalf("completion = %+v", completion)
	}
}

func TestClaudeJSONLRejectsProtocolFailure(t *testing.T) {
	for name, line := range map[string]string{
		"malformed":     `{`,
		"error result":  `{"type":"result","subtype":"error_during_execution","is_error":true,"result":"secret detail"}`,
		"wrong subtype": `{"type":"result","subtype":"interrupted","is_error":false}`,
	} {
		t.Run(name, func(t *testing.T) {
			var result claudeResult
			if err := result.consume([]byte(line)); err == nil {
				t.Fatal("consume accepted protocol failure")
			}
		})
	}
	if _, err := (claudeResult{}).completion(); err == nil {
		t.Fatal("completion accepted a missing final message")
	}
}
