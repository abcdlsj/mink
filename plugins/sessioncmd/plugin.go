package sessioncmd

import "github.com/abcdlsj/mink/app"

func Plugin() app.Plugin {
	return func(a *app.App) error {
		a.RegisterCommand(&sessionCmd{app: a})
		a.RegisterCommand(&compactCmd{app: a})
		a.RegisterCommand(&tokensCmd{app: a})
		a.RegisterCommand(&replayCmd{app: a})
		a.RegisterCommand(&inspectCmd{app: a})
		return nil
	}
}
