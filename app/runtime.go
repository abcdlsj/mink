package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/abcdlsj/sumi/agent"
	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/config"
	"github.com/abcdlsj/sumi/llm"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/persona"
	"github.com/abcdlsj/sumi/session"
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
		ProjectContext:       loadProjectContext(a.cfg.Workspace),
		SoulPath:             a.cfg.ResolvedSoulPath(),
		PreferencesPath:      a.cfg.PreferencesPath(),
		SkillCards:           a.skillCards(),
		ChildEnv:             a.cfg.ChildEnv(),
		Prompt:               a.cfg.Prompt,
		TelegramMentionMode:  a.cfg.Telegram.MentionMode,
		TelegramSessionScope: a.cfg.Telegram.SessionScope,
	}
}

func (a *App) skillCards() []string {
	if a == nil || a.skills == nil {
		return nil
	}
	return a.skills.Cards()
}

func (a *App) runtimeEnvFor(p *persona.Persona) *agent.RuntimeEnv {
	env := a.runtimeEnv()
	if p != nil {
		env.Persona = &agent.Persona{
			ID:          p.ID,
			Display:     p.Display,
			Description: p.Description,
			SoulPath:    p.SoulPath,
		}
	}
	return env
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

func (a *App) newRuntimeFor(name string, p *persona.Persona) (agent.Runtime, error) {
	f := a.runtimeFactory(name)
	if f == nil {
		return nil, fmt.Errorf("runtime not found: %s", name)
	}
	return f(a.runtimeEnvFor(p))
}

func (a *App) NewRuntimeFor(name string, p *persona.Persona) (agent.Runtime, error) {
	return a.newRuntimeFor(name, p)
}

func (a *App) runTurn(ctx context.Context, rt agent.Runtime, source, input string, attachments []msg.Attachment, s *session.Session) error {
	return a.runTurnAs(ctx, rt, source, "", input, attachments, s)
}

func (a *App) runTurnAs(ctx context.Context, rt agent.Runtime, source, personaID, input string, attachments []msg.Attachment, s *session.Session) error {
	return turnFlow{
		app:         a,
		runtime:     rt,
		source:      source,
		personaID:   personaID,
		input:       input,
		attachments: attachments,
		session:     s,
	}.run(ctx)
}

func (a *App) runTurnAsWithSpaceHistory(ctx context.Context, rt agent.Runtime, source, personaID, input string, attachments []msg.Attachment, s *session.Session) error {
	return turnFlow{
		app:                   a,
		runtime:               rt,
		source:                source,
		personaID:             personaID,
		input:                 input,
		attachments:           attachments,
		session:               s,
		includeHistory:        true,
		disableExternalResume: true,
	}.run(ctx)
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
