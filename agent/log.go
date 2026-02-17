package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/abcdlsj/mink/llm"
)

type reqLog struct {
	Ts         time.Time `json:"ts"`
	DurationMs int64     `json:"duration_ms"`
	Stream     bool      `json:"stream,omitempty"`
	MsgCount   int       `json:"msg_count"`
	System     string    `json:"system"`
	Tools      []string  `json:"tools,omitempty"`
}

type respLog struct {
	Ts      time.Time       `json:"ts"`
	Content string          `json:"content,omitempty"`
	Usage   *llm.TokenUsage `json:"usage,omitempty"`
}

func (a *Agent) logReq(system string, n int, stream bool, start time.Time) {
	if a.sessionDir == "" {
		return
	}
	e := reqLog{
		Ts:         start,
		DurationMs: time.Since(start).Milliseconds(),
		Stream:     stream,
		MsgCount:   n,
		System:     system,
		Tools:      a.toolNames(),
	}
	a.appendLog("_req.jsonl", e)
}

func (a *Agent) logResp(r *llm.Response) {
	if a.sessionDir == "" || r == nil {
		return
	}
	e := respLog{
		Ts:      time.Now(),
		Content: r.Content,
		Usage:   r.Usage,
	}
	a.appendLog("_resp.jsonl", e)
}

func (a *Agent) toolNames() []string {
	var names []string
	for _, t := range a.reg.All() {
		names = append(names, t.Name())
	}
	return names
}

func (a *Agent) appendLog(suffix string, v any) {
	path := filepath.Join(a.sessionDir, a.session.ID()+suffix)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()

	data, _ := json.Marshal(v)
	f.Write(append(data, '\n'))
}
