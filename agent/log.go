package agent

import (
	"time"

	"github.com/abcdlsj/mink/llm"
	"github.com/abcdlsj/mink/msg"
)

func (a *Agent) initTrace() {}

func (a *Agent) logSessionStart(source string) {}

func (a *Agent) logSessionEnd() {}

func (a *Agent) logUserInput(role, input string) {}

func (a *Agent) logInterrupt(reason string) {}

func (a *Agent) logStepStart(stepNum int) {}

func (a *Agent) logStepEnd(stepNum int, duration time.Duration, err error) {}

func (a *Agent) logLLMRequest(stepNum int, msgCount int, stream bool) string { return "" }

func (a *Agent) logLLMResponse(stepNum int, corr string, resp *llm.Response, duration time.Duration) {
}

func (a *Agent) logLLMError(stepNum int, corr string, err error, duration time.Duration) {}

func (a *Agent) logAgentOutput(stepNum int, content string) {}

func (a *Agent) logToolCall(stepNum int, tc msg.ToolCall) string { return "" }

func (a *Agent) logToolResult(stepNum int, corr string, tc msg.ToolCall, output string, err error, duration time.Duration) {
}

func (a *Agent) logWarn(eventType string, data map[string]any) {}

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
