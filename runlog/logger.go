package runlog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/abcdlsj/mink/llm"
	"github.com/abcdlsj/mink/msg"
)

const textPreviewLimit = 4000

type Level string

const (
	LevelDebug Level = "debug"
	LevelInfo  Level = "info"
	LevelWarn  Level = "warn"
	LevelError Level = "error"
)

type Event struct {
	ID            string         `json:"id"`
	Type          string         `json:"type"`
	Timestamp     time.Time      `json:"timestamp"`
	SessionID     string         `json:"session_id"`
	AgentID       string         `json:"agent_id,omitempty"`
	Source        string         `json:"source,omitempty"`
	StepNum       *int           `json:"step_num,omitempty"`
	Level         Level          `json:"level"`
	CorrelationID string         `json:"correlation_id,omitempty"`
	DurationMs    int64          `json:"duration_ms,omitempty"`
	Data          map[string]any `json:"data,omitempty"`
}

type Logger struct {
	dir       string
	sessionID string
	agentID   string
	source    string
	mu        sync.Mutex
}

func New(dir, sessionID, agentID string) *Logger {
	return &Logger{dir: dir, sessionID: sessionID, agentID: agentID}
}

func (l *Logger) Enabled() bool {
	return strings.TrimSpace(l.dir) != "" && strings.TrimSpace(l.sessionID) != ""
}

func (l *Logger) WithSource(source string) {
	l.mu.Lock()
	l.source = source
	l.mu.Unlock()
}

func (l *Logger) SessionStart(source string) {
	l.WithSource(source)
	l.log(LevelInfo, "session_start", 0, "", 0, map[string]any{"source": source})
}

func (l *Logger) SessionEnd() {
	l.log(LevelInfo, "session_end", 0, "", 0, nil)
}

func (l *Logger) StepStart(stepNum int) {
	l.log(LevelInfo, "step_start", stepNum, "", 0, nil)
}

func (l *Logger) StepEnd(stepNum int, duration time.Duration, err error) {
	data := map[string]any{}
	if err != nil {
		data["error"] = err.Error()
	}
	l.log(levelForErr(err, LevelInfo), "step_end", stepNum, "", duration, emptyToNil(data))
}

func (l *Logger) UserInput(role, input string) {
	l.log(LevelInfo, "user_input", 0, "", 0, map[string]any{
		"role":       role,
		"input":      truncate(input),
		"input_size": textSize(input),
	})
}

func (l *Logger) AgentOutput(stepNum int, content string, stream bool) {
	l.log(LevelInfo, "agent_output", stepNum, "", 0, map[string]any{
		"content":      truncate(content),
		"content_size": textSize(content),
		"stream":       stream,
	})
}

func (l *Logger) LLMRequest(stepNum int, provider, model string, msgCount int, stream bool, tools []string) string {
	corr := newID("req")
	l.log(LevelDebug, "llm_request", stepNum, corr, 0, map[string]any{
		"provider":      provider,
		"model":         model,
		"message_count": msgCount,
		"stream":        stream,
		"tools":         tools,
	})
	return corr
}

func (l *Logger) LLMResponse(stepNum int, corr string, resp *llm.Response, duration time.Duration) {
	if resp == nil {
		return
	}
	l.log(LevelDebug, "llm_response", stepNum, corr, duration, map[string]any{
		"content":             truncate(resp.Content),
		"content_size":        textSize(resp.Content),
		"reasoning_size":      textSize(resp.Reasoning),
		"reasoning_signature": resp.ReasoningSignature != "",
		"tool_call_count":     len(resp.ToolCalls),
		"usage":               resp.Usage,
	})
}

func (l *Logger) LLMError(stepNum int, corr string, err error, duration time.Duration) {
	if err == nil {
		return
	}
	l.log(LevelError, "llm_error", stepNum, corr, duration, map[string]any{"error": err.Error()})
}

func (l *Logger) ToolCall(stepNum int, tc msg.ToolCall) string {
	corr := newID("tool")
	l.log(LevelInfo, "tool_call", stepNum, corr, 0, map[string]any{
		"tool_call_id": tc.ID,
		"name":         tc.Name,
		"args":         compactJSON(tc.Args),
	})
	return corr
}

func (l *Logger) ToolResult(stepNum int, corr, name, output string, err error, duration time.Duration) {
	data := map[string]any{
		"name":        name,
		"output":      truncate(output),
		"output_size": textSize(output),
	}
	if err != nil {
		data["error"] = err.Error()
	}
	l.log(levelForErr(err, LevelInfo), "tool_end", stepNum, corr, duration, data)
}

func (l *Logger) Interrupt(reason string) {
	l.log(LevelWarn, "interrupt", 0, "", 0, map[string]any{"reason": reason})
}

func (l *Logger) Warn(eventType string, data map[string]any) {
	l.log(LevelWarn, eventType, 0, "", 0, emptyToNil(data))
}

func (l *Logger) log(level Level, eventType string, stepNum int, corr string, duration time.Duration, data map[string]any) {
	if !l.Enabled() {
		return
	}
	e := Event{
		ID:            newID("evt"),
		Type:          eventType,
		Timestamp:     time.Now(),
		SessionID:     l.sessionID,
		AgentID:       l.agentID,
		Level:         level,
		CorrelationID: corr,
		DurationMs:    duration.Milliseconds(),
		Data:          data,
	}
	l.mu.Lock()
	e.Source = l.source
	l.mu.Unlock()
	if stepNum >= 0 && (eventType == "step_start" || eventType == "step_end" || eventType == "llm_request" || eventType == "llm_response" || eventType == "llm_error" || eventType == "tool_call" || eventType == "tool_end" || eventType == "agent_output") {
		step := stepNum
		e.StepNum = &step
	}
	l.append(e)
}

func (l *Logger) append(event Event) {
	if err := os.MkdirAll(l.dir, 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(l.dir, l.sessionID+".log.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()

	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	_, _ = f.Write(append(data, '\n'))
}

func compactJSON(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var decoded any
	if json.Unmarshal(raw, &decoded) == nil {
		return decoded
	}
	return truncate(string(raw))
}

func emptyToNil(data map[string]any) map[string]any {
	if len(data) == 0 {
		return nil
	}
	return data
}

func newID(prefix string) string {
	return prefix + "_" + uuid.NewString()[:8]
}

func levelForErr(err error, fallback Level) Level {
	if err != nil {
		return LevelError
	}
	return fallback
}

func truncate(text string) string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) <= textPreviewLimit {
		return string(runes)
	}
	return string(runes[:textPreviewLimit]) + "..."
}

func textSize(text string) int {
	return len([]rune(text))
}
