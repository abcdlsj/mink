package codex

import (
	"github.com/abcdlsj/sumi/app"
	"github.com/abcdlsj/sumi/plugins/external"
)

func Plugin() app.Plugin {
	return func(a *app.App) error {
		a.RegisterRuntime("codex", external.NewRuntime(driver()))
		return nil
	}
}
