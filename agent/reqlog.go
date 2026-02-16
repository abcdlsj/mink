package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/abcdlsj/mink/llm"
)

type reqEntry struct {
	Timestamp  time.Time       `json:"ts"`
	DurationMs int64           `json:"duration_ms"`
	Stream     bool            `json:"stream,omitempty"`
	MsgCount   int             `json:"msg_count"`
	System     string          `json:"system"`
	Tools      []string        `json:"tools,omitempty"`
	Usage      *llm.TokenUsage `json:"usage,omitempty"`
}

func (a *Agent) logReq(system string, n int, stream bool, start time.Time, r *llm.Response) {
	if a.sessionDir == "" {
		return
	}

	var tools []string
	for _, t := range a.reg.All() {
		tools = append(tools, t.Name())
	}

	e := reqEntry{
		Timestamp:  start,
		DurationMs: time.Since(start).Milliseconds(),
		Stream:     stream,
		MsgCount:   n,
		System:     system,
		Tools:      tools,
	}
	if r != nil {
		e.Usage = r.Usage
	}

	path := filepath.Join(a.sessionDir, a.session.ID()+"_req.json")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()

	data, err := json.Marshal(e)
	if err != nil {
		return
	}
	f.Write(append(data, '\n'))
}
