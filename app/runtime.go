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

func providerFromModel(mc config.ModelConfig) (llm.Provider, error) {
	if strings.TrimSpace(mc.Provider) == "" || strings.TrimSpace(mc.Model) == "" {
		return nil, fmt.Errorf("model %s missing provider or model", mc.Model)
	}
	return llm.NewProvider(llm.Config{
		Provider:  mc.Provider,
		APIKey:    mc.APIKey,
		BaseURL:   mc.BaseURL,
		Model:     mc.Model,
		Headers:   mc.Headers,
		MaxTokens: mc.MaxTokens,
	})
}

func (a *App) visionProvider() (llm.Provider, string, error) {
	if a == nil {
		return nil, "", nil
	}
	name := strings.TrimSpace(a.cfg.Vision)
	if name == "" {
		return nil, "", nil
	}
	mc, ok := a.cfg.NamedModel(name)
	if !ok {
		return nil, "", fmt.Errorf("vision_model=%q not registered", name)
	}
	p, err := providerFromModel(mc)
	if err != nil {
		return nil, "", err
	}
	return p, mc.Provider + " / " + mc.Model, nil
}

func (a *App) runtimeEnv() *agent.RuntimeEnv {
	return &agent.RuntimeEnv{
		Provider:             a.provider,
		Tools:                a.tools,
		DataRoot:             a.cfg.DataRoot(),
		Workspace:            a.cfg.Workspace,
		MemoryRoot:           a.cfg.MemoryDir(),
		ProjectContext:       loadProjectContext(a.cfg.Workspace),
		SoulPath:             a.cfg.ResolvedSoulPath(),
		PreferencesPath:      a.cfg.PreferencesPath(),
		SkillCards:           a.skillCards(),
		ChildEnv:             a.cfg.ExternalRuntimeEnv(),
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
			ID:           p.ID,
			Display:      p.Display,
			Description:  p.Description,
			Capabilities: append([]string(nil), p.Capabilities...),
			TaskPolicy:   p.TaskPolicy,
			MemoryPolicy: p.MemoryPolicy,
			SoulPath:     p.SoulPath,
		}
	}
	return env
}

func (a *App) runtimeEnvForTurn(p *persona.Persona, attachments []msg.Attachment) (*agent.RuntimeEnv, string) {
	env := a.runtimeEnvFor(p)
	if !hasImageAttachment(attachments) {
		return env, ""
	}
	provider, label, err := a.visionProvider()
	if err != nil {
		a.bus.Publish(bus.Event{Type: bus.ServiceNotice, Text: "vision_model: " + err.Error()})
		return env, ""
	}
	if provider == nil {
		return env, ""
	}
	env.Provider = provider
	return env, label
}

func hasImageAttachment(attachments []msg.Attachment) bool {
	for _, a := range attachments {
		if a.Kind != "image" {
			continue
		}
		if a.URL != "" || (a.Data != "" && a.MIME != "") {
			return true
		}
	}
	return false
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

// effectiveRuntimeName resolves the runtime that will actually consume a turn,
// applying the same empty->cfg.Runtime fallback as runtimeFactory. Callers MUST
// use this single resolved name for everything keyed on the consumer's identity
// — the overflow preflight budget (hardBudgetStatusFor), the runtime build, and
// the external-vs-native memory branch — so an empty persona runtime under a
// global external cfg.Runtime is never mistaken for native. It does not apply
// permission remapping; entrypoints that need it wrap the result with
// runtimeForPermission.
func (a *App) effectiveRuntimeName(name string) string {
	if n := strings.TrimSpace(name); n != "" {
		return n
	}
	return strings.TrimSpace(a.cfg.Runtime)
}

func (a *App) newRuntimeFor(name string, p *persona.Persona) (agent.Runtime, error) {
	f := a.runtimeFactory(name)
	if f == nil {
		return nil, fmt.Errorf("runtime not found: %s", name)
	}
	return f(a.runtimeEnvFor(p))
}

func (a *App) newRuntimeForTurn(name string, p *persona.Persona, attachments []msg.Attachment) (agent.Runtime, string, error) {
	f := a.runtimeFactory(name)
	if f == nil {
		return nil, "", fmt.Errorf("runtime not found: %s", name)
	}
	env, label := a.runtimeEnvForTurn(p, attachments)
	rt, err := f(env)
	if err != nil {
		return nil, "", err
	}
	return rt, label, nil
}

func (a *App) NewRuntimeFor(name string, p *persona.Persona) (agent.Runtime, error) {
	return a.newRuntimeFor(name, p)
}

func (a *App) runTurn(ctx context.Context, rt agent.Runtime, source, input string, attachments []msg.Attachment, s *session.Session) error {
	return a.runTurnAs(ctx, rt, source, "", input, attachments, s)
}

func (a *App) runTurnAs(ctx context.Context, rt agent.Runtime, source, personaID, input string, attachments []msg.Attachment, s *session.Session) error {
	return a.runTurnAsNamed(ctx, rt, "", source, personaID, input, attachments, s)
}

func (a *App) runTurnAsNamed(ctx context.Context, rt agent.Runtime, runtimeName, source, personaID, input string, attachments []msg.Attachment, s *session.Session) error {
	return turnFlow{
		app:         a,
		runtime:     rt,
		runtimeName: runtimeName,
		source:      source,
		personaID:   personaID,
		input:       input,
		attachments: attachments,
		session:     s,
	}.run(ctx)
}

func (a *App) runTurnAsWithSpaceHistory(ctx context.Context, rt agent.Runtime, source, personaID, input string, attachments []msg.Attachment, s *session.Session) error {
	return a.runTurnAsWithSpaceHistoryNamed(ctx, rt, "", source, personaID, input, attachments, s)
}

func (a *App) runTurnAsWithSpaceHistoryNamed(ctx context.Context, rt agent.Runtime, runtimeName, source, personaID, input string, attachments []msg.Attachment, s *session.Session) error {
	return turnFlow{
		app:                   a,
		runtime:               rt,
		runtimeName:           runtimeName,
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
	if next.ResolveNamedModel(provider, model) {
		// Configured model names carry provider/base_url/api_key; optional model overrides only the model id.
	} else if strings.TrimSpace(model) != "" {
		next.Resolve(provider, model)
	} else {
		return fmt.Errorf("model %q is not configured", strings.TrimSpace(provider))
	}
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
