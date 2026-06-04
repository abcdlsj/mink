package main

import (
	"context"
	"fmt"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"

	"github.com/abcdlsj/sumi/app"
	"github.com/abcdlsj/sumi/config"
	pluginsbackground "github.com/abcdlsj/sumi/plugins/background"
	pluginsclaude "github.com/abcdlsj/sumi/plugins/claude"
	pluginscodex "github.com/abcdlsj/sumi/plugins/codex"
	pluginscollab "github.com/abcdlsj/sumi/plugins/collab"
	pluginscron "github.com/abcdlsj/sumi/plugins/cron"
	pluginsdesktop "github.com/abcdlsj/sumi/plugins/desktop"
	pluginsmemory "github.com/abcdlsj/sumi/plugins/memory"
	pluginsnotify "github.com/abcdlsj/sumi/plugins/notify"
	pluginspersona "github.com/abcdlsj/sumi/plugins/persona"
	pluginssearch "github.com/abcdlsj/sumi/plugins/search"
	pluginssessioncmd "github.com/abcdlsj/sumi/plugins/sessioncmd"
)

func main() {
	cfg := config.Load()
	a, err := app.New(cfg)
	if err != nil {
		fail(err)
	}
	defer a.Close()
	if err := a.Use(plugins()...); err != nil {
		fail(err)
	}

	bridge := pluginsdesktop.NewWailsBridge(a)
	defer bridge.Close()

	err = wails.Run(&options.App{
		Title:     "Sumi",
		Width:     1180,
		Height:    760,
		MinWidth:  900,
		MinHeight: 560,
		AssetServer: &assetserver.Options{
			Assets:  bridge.Assets(),
			Handler: bridge.Handler(),
		},
		BackgroundColour: &options.RGBA{R: 245, G: 246, B: 248, A: 255},
		OnStartup: func(ctx context.Context) {
			bridge.Start(ctx)
		},
		Bind: []any{bridge},
		Mac: &mac.Options{
			TitleBar: mac.TitleBarHiddenInset(),
			About: &mac.AboutInfo{
				Title:   "Sumi",
				Message: "Workspace-native collaboration for agents.",
			},
		},
	})
	if err != nil {
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
		pluginsnotify.Plugin(),
		pluginspersona.Plugin(),
		pluginssearch.Plugin(),
		pluginssessioncmd.Plugin(),
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
