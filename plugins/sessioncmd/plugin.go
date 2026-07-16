package sessioncmd

import "github.com/abcdlsj/sumi/app"

func Plugin() app.Plugin {
	return func(a *app.App) error {
		a.RegisterCommand(&sessionCmd{app: a})
		// NOTE: /compact is intentionally NOT registered here. The authoritative
		// manual-compact lives in core (app.runCompactCommand): LLM summarize,
		// configurable keep, SessionCompacted event — same semantics as auto-compact.
		a.RegisterCommand(&tokensCmd{app: a})
		a.RegisterCommand(&replayCmd{app: a})
		a.RegisterCommand(&inspectCmd{app: a})
		return nil
	}
}
