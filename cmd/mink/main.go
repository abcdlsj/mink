package main

import (
	"context"
	"fmt"
	"os"

	"github.com/abcdlsj/mink/app"
	"github.com/abcdlsj/mink/config"
	pluginsbackground "github.com/abcdlsj/mink/plugins/background"
	pluginsclaude "github.com/abcdlsj/mink/plugins/claude"
	pluginscodex "github.com/abcdlsj/mink/plugins/codex"
	pluginscollab "github.com/abcdlsj/mink/plugins/collab"
	pluginscron "github.com/abcdlsj/mink/plugins/cron"
	pluginsmemory "github.com/abcdlsj/mink/plugins/memory"
	pluginssearch "github.com/abcdlsj/mink/plugins/search"
	pluginssessioncmd "github.com/abcdlsj/mink/plugins/sessioncmd"
	pluginstelegram "github.com/abcdlsj/mink/plugins/telegram"
	pluginsweb "github.com/abcdlsj/mink/plugins/web"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version":
			runVersion()
			return
		case "mcp-bridge":
			if err := runBridge(os.Args[2:]); err != nil {
				fail(err)
			}
			return
		}
	}

	cfg := config.Load()
	a, err := app.New(cfg)
	if err != nil {
		fail(err)
	}
	defer a.Close()
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
		pluginsmemory.Plugin(),
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

func errf(format string, args ...any) error { return fmt.Errorf(format, args...) }
