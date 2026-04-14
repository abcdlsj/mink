package mink

import (
	"context"
	"fmt"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/platform"
	"github.com/abcdlsj/mink/session"
)

func workspacePlatformSource(kind, workspace string) string {
	_ = workspace
	return bus.Platform(kind)
}

func (a *App) cliSource() string {
	if a == nil {
		return bus.AddrPlatformCLI
	}
	return workspacePlatformSource("cli", a.workspace)
}

func (a *App) StartCLI(ctx context.Context) error {
	if err := a.Start(ctx); err != nil {
		return err
	}

	a.mu.Lock()
	if a.cli != nil {
		a.mu.Unlock()
		return nil
	}
	runCtx := a.ctx
	a.mu.Unlock()

	cliSource := a.cliSource()
	if _, err := a.prepareFreshSource(runCtx, cliSource); err != nil {
		return err
	}

	cli := platform.NewCLI(a.bus, a.router, a.hooks, a.cliStatus(), a.cliSessionMessages(cliSource), cliSource)
	if err := cli.Start(runCtx); err != nil {
		return err
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		_ = cli.Stop()
		return ErrClosed
	}
	if a.cli != nil {
		_ = cli.Stop()
		return nil
	}

	a.cli = cli
	a.adapters = append(a.adapters, cli)
	a.guard.Register(cliSource, cli)
	return nil
}

func (a *App) RunCLI(ctx context.Context) error {
	if err := a.StartCLI(ctx); err != nil {
		return err
	}

	a.mu.Lock()
	cli := a.cli
	a.mu.Unlock()
	if cli == nil {
		return fmt.Errorf("cli adapter not initialized")
	}

	return cli.Run()
}

func (a *App) StartWeb(ctx context.Context, addr string) error {
	if err := a.Start(ctx); err != nil {
		return err
	}

	a.mu.Lock()
	if a.web != nil {
		a.mu.Unlock()
		return nil
	}
	runCtx := a.ctx
	a.mu.Unlock()

	webSource := workspacePlatformSource("web", a.workspace)
	sessionID, err := a.prepareFreshSource(runCtx, webSource)
	if err != nil {
		return err
	}
	_ = a.sm.Update(sessionID, func(s *session.Session) {
		s.SetKind("main")
		s.SetStatus("active")
	})
	a.setMainSession(webSource, sessionID)
	a.setActiveSection(webSource, "main")

	web := platform.NewWeb(addr, platform.WebCallbacks{
		State: func() (platform.WebState, error) {
			return a.webState(runCtx, webSource)
		},
		Select: func(section, id string) error {
			return a.webSelect(runCtx, webSource, section, id)
		},
		SendMessage: func(text string) error {
			return a.webSendMessage(runCtx, webSource, text)
		},
		NewSession: func() error {
			return a.webNewSession(runCtx, webSource)
		},
		Action: func(name string) error {
			return a.webAction(runCtx, webSource, name)
		},
	})

	if staticDir := findWebDist(); staticDir != "" {
		web.SetStaticDir(staticDir)
	}

	if err := web.Start(runCtx); err != nil {
		return err
	}

	observeCh := make(chan bus.Msg, 256)
	a.bus.Observe(observeCh)
	go func() {
		defer a.bus.Unobserve(observeCh)
		for {
			select {
			case <-runCtx.Done():
				return
			case m := <-observeCh:
				if m.From != webSource && m.To != webSource {
					continue
				}
				web.NotifyStateChanged()
			}
		}
	}()

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		_ = web.Stop()
		return ErrClosed
	}
	if a.web != nil {
		_ = web.Stop()
		return nil
	}

	a.web = web
	a.adapters = append(a.adapters, web)
	return nil
}

func (a *App) prepareFreshSource(ctx context.Context, src string) (string, error) {
	if a.disp != nil {
		a.disp.InvalidateSource(src)
		a.disp.UnbindTeamSource(src)
	}
	a.setActiveTeam(src, "")
	a.setActiveThread(src, "")
	a.setActiveSection(src, "")

	if a.rt != nil {
		if err := a.rt.ResetSource(ctx, src); err != nil {
			return "", err
		}
	}
	if a.sm != nil {
		sess, err := a.sm.ResetSource(src)
		if err != nil {
			return "", err
		}
		return sess.ID(), nil
	}
	return "", nil
}

func (a *App) StartTelegram(ctx context.Context, token string) error {
	if token == "" {
		token = a.cfg.Key("TELEGRAM_TOKEN")
	}
	if token == "" {
		return fmt.Errorf("tg mode need telegram token")
	}

	a.mu.Lock()
	if a.cfg.Mode != "tg" {
		a.cfg.Mode = "tg"
		a.disp.SetConfig(a.cfg)
	}
	a.mu.Unlock()

	if err := a.Start(ctx); err != nil {
		return err
	}

	a.mu.Lock()
	if a.telegram != nil && a.telegram.Token() == token {
		a.mu.Unlock()
		return nil
	}
	oldTG := a.telegram
	if oldTG != nil {
		a.telegram = nil
		a.adapters = removeAdapter(a.adapters, oldTG.ID())
		a.guard.Unregister("telegram:")
	}
	runCtx := a.ctx
	a.mu.Unlock()

	if oldTG != nil {
		_ = oldTG.Stop()
	}

	tg := platform.NewTelegram(token, a.bus, a.router, platform.TelegramOptions{
		MentionMode:  a.cfg.TelegramMentionMode,
		SessionScope: a.cfg.TelegramSessionScope,
	})
	if a.reg != nil {
		names := make(map[string]string)
		for _, s := range a.reg.All() {
			names[s.Descriptor.Name] = s.Descriptor.ID
		}
		tg.SetAgentNames(names)
	}
	if err := tg.Start(runCtx); err != nil {
		return err
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		_ = tg.Stop()
		return ErrClosed
	}
	if a.telegram != nil {
		_ = tg.Stop()
		return nil
	}

	a.telegram = tg
	a.adapters = append(a.adapters, tg)
	a.guard.Register("telegram:", tg)
	return nil
}

func (a *App) Close() error {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil
	}
	a.closed = true

	cancel := a.cancel
	adapters := append([]platform.Adapter(nil), a.adapters...)
	a.adapters = nil
	a.cli = nil
	a.telegram = nil
	a.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if a.sup != nil {
		_ = a.sup.Stop()
	}
	if a.hb != nil {
		a.hb.Stop()
	}
	if a.rt != nil {
		_ = a.rt.Close()
	}

	for _, ad := range adapters {
		_ = ad.Stop()
	}

	return nil
}

func removeAdapter(adapters []platform.Adapter, id string) []platform.Adapter {
	if len(adapters) == 0 {
		return nil
	}
	out := adapters[:0]
	for _, ad := range adapters {
		if ad == nil || ad.ID() == id {
			continue
		}
		out = append(out, ad)
	}
	return out
}
