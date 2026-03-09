package agent

import (
	"strings"
	"time"

	"github.com/abcdlsj/mink/llm"
	"github.com/abcdlsj/mink/msg"
	"github.com/abcdlsj/mink/runlog"
)

func (a *Agent) initTrace() {
	if a.session == nil {
		return
	}
	a.trace = runlog.New(a.sessionDir, a.session.ID(), a.id)
}

func (a *Agent) logSessionStart(source string) {
	if a.trace == nil {
		return
	}
	a.trace.SessionStart(source)
}

func (a *Agent) logSessionEnd() {
	if a.trace == nil {
		return
	}
	a.trace.SessionEnd()
}

func (a *Agent) logUserInput(role, input string) {
	if a.trace == nil {
		return
	}
	a.trace.UserInput(role, input)
}

func (a *Agent) logInterrupt(reason string) {
	if a.trace == nil {
		return
	}
	a.trace.Interrupt(reason)
}

func (a *Agent) logStepStart(stepNum int) {
	if a.trace == nil {
		return
	}
	a.trace.StepStart(stepNum)
}

func (a *Agent) logStepEnd(stepNum int, duration time.Duration, err error) {
	if a.trace == nil {
		return
	}
	a.trace.StepEnd(stepNum, duration, err)
}

func (a *Agent) logLLMRequest(stepNum int, msgCount int, stream bool) string {
	if a.trace == nil {
		return ""
	}
	provider, model := a.selectedModelInfo()
	return a.trace.LLMRequest(stepNum, provider, model, msgCount, stream, a.toolNames())
}

func (a *Agent) logLLMResponse(stepNum int, corr string, resp *llm.Response, duration time.Duration) {
	if a.trace == nil {
		return
	}
	a.trace.LLMResponse(stepNum, corr, resp, duration)
}

func (a *Agent) logLLMError(stepNum int, corr string, err error, duration time.Duration) {
	if a.trace == nil {
		return
	}
	a.trace.LLMError(stepNum, corr, err, duration)
}

func (a *Agent) logAgentOutput(stepNum int, content string) {
	if a.trace == nil || strings.TrimSpace(content) == "" {
		return
	}
	a.trace.AgentOutput(stepNum, content, a.stream)
}

func (a *Agent) logToolCall(stepNum int, tc msg.ToolCall) string {
	if a.trace == nil {
		return ""
	}
	return a.trace.ToolCall(stepNum, tc)
}

func (a *Agent) logToolResult(stepNum int, corr string, tc msg.ToolCall, output string, err error, duration time.Duration) {
	if a.trace == nil {
		return
	}
	a.trace.ToolResult(stepNum, corr, tc.Name, output, err, duration)
}

func (a *Agent) logWarn(eventType string, data map[string]any) {
	if a.trace == nil {
		return
	}
	a.trace.Warn(eventType, data)
}

func (a *Agent) toolNames() []string {
	var names []string
	for _, t := range a.reg.All() {
		names = append(names, t.Name())
	}
	return names
}

func (a *Agent) selectedModelInfo() (string, string) {
	if a.nextModel == "cheap" {
		cfg := a.cfg
		if cfg.ResolveCheapModel() {
			return cfg.Active.Provider, cfg.Active.Model
		}
	}
	return a.cfg.Active.Provider, a.cfg.Active.Model
}
