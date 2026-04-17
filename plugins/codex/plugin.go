package codex

import (
	"github.com/abcdlsj/mink/app"
	"github.com/abcdlsj/mink/plugins/external"
)

func Plugin() app.Plugin {
	return func(a *app.App) error {
		a.RegisterRuntime("codex", external.NewRuntime(driver()))
		return nil
	}
}
