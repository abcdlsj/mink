package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/abcdlsj/mink/agent"
	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/config"
	"github.com/abcdlsj/mink/llm"
	"github.com/abcdlsj/mink/session"
)

func newProvider(cfg config.Config) (llm.Provider, error) {
	if !cfg.Ready() {
		return nil, nil
	}
	return llm.NewProvider(llm.Config{
		Provider:  cfg.Provider,
		APIKey:    cfg.APIKey,
		BaseURL:   cfg.BaseURL,
		Model:     cfg.Model,
		Headers:   cfg.Headers,
		MaxTokens: cfg.MaxTokens,
	})
}

func (a *App) runtimeEnv() *agent.RuntimeEnv {
	return &agent.RuntimeEnv{
		Provider:             a.provider,
		Tools:                a.tools,
		Workspace:            a.cfg.Workspace,
		SoulPath:             a.cfg.ResolvedSoulPath(),
		Prompt:               a.cfg.Prompt,
		TelegramMentionMode:  a.cfg.Telegram.MentionMode,
		TelegramSessionScope: a.cfg.Telegram.SessionScope,
		MaxSteps:             8,
	}
}

func (a *App) runtimeFactory(name string) agent.RuntimeFactory {
	name = strings.TrimSpace(name)
	if name == "" {
		name = a.cfg.Runtime
	}
	if f := a.runtimes[name]; f != nil {
		return f
	}
	return a.runtimes["native"]
}

func (a *App) newRuntime(name string) (agent.Runtime, error) {
	f := a.runtimeFactory(name)
	if f == nil {
		return nil, fmt.Errorf("runtime not found: %s", name)
	}
	return f(a.runtimeEnv())
}

func (a *App) runTurn(ctx context.Context, rt agent.Runtime, source, input string, s *session.Session) error {
	a.bus.Publish(bus.Event{Type: bus.TurnStarted, Source: source, SessionID: s.ID})
	runErr := rt.Run(ctx, &agent.Turn{
		Source:  source,
		Input:   input,
		Session: s,
		Bus:     a.bus,
	})
	saveErr := a.sessions.Save(s)
	if runErr != nil {
		err := turnErr(runErr, saveErr)
		a.bus.Publish(bus.Event{Type: bus.TurnError, Source: source, SessionID: s.ID, Err: err.Error()})
		if saveErr == nil {
			a.bus.Publish(bus.Event{Type: bus.SessionUpdated, Source: source, SessionID: s.ID})
		}
		return err
	}
	if saveErr != nil {
		return saveErr
	}
	a.bus.Publish(bus.Event{Type: bus.SessionUpdated, Source: source, SessionID: s.ID})
	a.bus.Publish(bus.Event{Type: bus.TurnFinished, Source: source, SessionID: s.ID})
	return nil
}

func turnErr(runErr, saveErr error) error {
	if runErr == nil {
		return saveErr
	}
	if saveErr == nil {
		return runErr
	}
	return fmt.Errorf("%w; save session: %v", runErr, saveErr)
}

func latestAssistant(s *session.Session) string {
	if s == nil {
		return ""
	}
	for i := len(s.Messages) - 1; i >= 0; i-- {
		if s.Messages[i].Role == "assistant" {
			return s.Messages[i].Content
		}
	}
	return ""
}

func (a *App) switchModel(provider, model string) error {
	next := a.cfg
	next.Resolve(provider, model)
	if !next.Ready() {
		return fmt.Errorf("provider %s is not configured", provider)
	}
	p, err := newProvider(next)
	if err != nil {
		return err
	}
	a.cfg = next
	a.provider = p
	a.bus.Publish(bus.Event{
		Type:   bus.ModelChanged,
		Source: "system",
		Text:   a.currentModel(),
	})
	return nil
}

func (a *App) currentModel() string {
	if a.cfg.Provider == "" {
		return "(unconfigured)"
	}
	return a.cfg.Provider + " / " + a.cfg.Model
}
