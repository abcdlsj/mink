package main

import (
	"context"
	"fmt"
	"os"

	"github.com/abcdlsj/sumi/app"
	"github.com/abcdlsj/sumi/cli"
	"github.com/abcdlsj/sumi/config"
	pluginsbackground "github.com/abcdlsj/sumi/plugins/background"
	pluginsclaude "github.com/abcdlsj/sumi/plugins/claude"
	pluginscodex "github.com/abcdlsj/sumi/plugins/codex"
	pluginscollab "github.com/abcdlsj/sumi/plugins/collab"
	pluginscron "github.com/abcdlsj/sumi/plugins/cron"
	pluginsdesktop "github.com/abcdlsj/sumi/plugins/desktop"
	pluginsmemory "github.com/abcdlsj/sumi/plugins/memory"
	pluginspersona "github.com/abcdlsj/sumi/plugins/persona"
	pluginssearch "github.com/abcdlsj/sumi/plugins/search"
	pluginssessioncmd "github.com/abcdlsj/sumi/plugins/sessioncmd"
	pluginstelegram "github.com/abcdlsj/sumi/plugins/telegram"
	pluginsweb "github.com/abcdlsj/sumi/plugins/web"
)

var (
	Version   = "dev"
	Commit    = "unknown"
	BuildTime = "unknown"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version":
			runVersion()
			return
		}
	}

	cfg := config.Load()
	a, err := app.New(cfg)
	if err != nil {
		fail(err)
	}
	defer a.Close()
	a.RegisterEntrypoint("cli", cli.Run)
	if err := a.Use(plugins()...); err != nil {
		fail(err)
	}
	if err := a.Run(context.Background(), os.Args[1:]); err != nil {
		fail(err)
	}
}

func plugins() []app.Plugin {
	return []app.Plugin{
		pluginsbackground.Plugin(),
		pluginsclaude.Plugin(),
		pluginscollab.Plugin(),
		pluginscron.Plugin(),
		pluginscodex.Plugin(),
		pluginsdesktop.Plugin(),
		pluginsmemory.Plugin(),
		pluginspersona.Plugin(),
		pluginssearch.Plugin(),
		pluginssessioncmd.Plugin(),
		pluginstelegram.Plugin(),
		pluginsweb.Plugin(),
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

func runVersion() {
	fmt.Printf("sumi version %s\n", Version)
	fmt.Printf("  commit: %s\n", Commit)
	fmt.Printf("  built:  %s\n", BuildTime)
}
