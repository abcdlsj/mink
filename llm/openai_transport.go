package llm

import (
	"encoding/json"
	"io"
	"net/http"
)

type openAITransport struct {
	headers   map[string]string
	reasoning bool
}

func (t *openAITransport) prepare(req *http.Request) error {
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	if req.Body == nil {
		return nil
	}
	body, err := io.ReadAll(req.Body)
	req.Body.Close()
	if err != nil {
		return err
	}
	body = patchAssistantContent(body)
	if t.reasoning {
		body = patchReasoning(body)
	}
	resetRequestBody(req, body)
	return nil
}

func patchReasoning(body []byte) []byte {
	var obj map[string]json.RawMessage
	if json.Unmarshal(body, &obj) != nil {
		return body
	}
	if _, ok := obj["reasoning"]; ok {
		return body
	}
	obj["reasoning"] = json.RawMessage(`{"enabled":true}`)
	out, _ := json.Marshal(obj)
	return out
}

func patchAssistantContent(body []byte) []byte {
	var obj map[string]json.RawMessage
	if json.Unmarshal(body, &obj) != nil {
		return body
	}
	raw, ok := obj["messages"]
	if !ok {
		return body
	}
	var msgs []map[string]json.RawMessage
	if json.Unmarshal(raw, &msgs) != nil {
		return body
	}
	patched := false
	for _, m := range msgs {
		if string(m["role"]) == `"assistant"` {
			if _, ok := m["content"]; !ok {
				m["content"] = json.RawMessage("null")
				patched = true
			}
		}
	}
	if !patched {
		return body
	}
	obj["messages"], _ = json.Marshal(msgs)
	out, _ := json.Marshal(obj)
	return out
}
