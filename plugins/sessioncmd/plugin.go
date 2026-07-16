package sessioncmd

import (
	"github.com/abcdlsj/sumi/app"
	"github.com/abcdlsj/sumi/command"
)

func Plugin() app.Plugin {
	return func(a *app.App) error {
		// NOTE: /compact is intentionally NOT registered here. The authoritative
		// manual-compact lives in core (app.runCompactCommand): LLM summarize,
		// configurable keep, SessionCompacted event — same semantics as auto-compact.
		cmds := []command.Command{
			&sessionCmd{app: a},
			&tokensCmd{app: a},
			&replayCmd{app: a},
			&inspectCmd{app: a},
		}
		for _, c := range cmds {
			if err := a.RegisterCommand(c); err != nil {
				return err
			}
		}
		return nil
	}
}
