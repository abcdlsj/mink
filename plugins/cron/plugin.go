package cron

import (
	"context"

	"github.com/abcdlsj/sumi/app"
)

func Plugin() app.Plugin {
	return func(a *app.App) error {
		s := &scheduler{
			app:  a,
			path: a.Config().CronPath(),
		}
		a.RegisterTool(&toolImpl{s: s})
		a.RegisterService("cron", func(ctx context.Context, _ *app.App) error {
			return s.Start(ctx)
		})
		return nil
	}
}
