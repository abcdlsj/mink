package mink

import core "github.com/abcdlsj/mink/app"

var (
	ErrClosed         = core.ErrClosed
	ErrAPIKeyRequired = core.ErrAPIKeyRequired

	Version   = "dev"
	Commit    = "unknown"
	BuildTime = "unknown"
)

type Options = core.Options
type App = core.App

func New(opts Options) (*App, error) {
	return core.New(opts)
}
