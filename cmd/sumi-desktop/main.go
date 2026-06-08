package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

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
	pluginspersona "github.com/abcdlsj/sumi/plugins/persona"
	pluginssearch "github.com/abcdlsj/sumi/plugins/search"
	pluginssessioncmd "github.com/abcdlsj/sumi/plugins/sessioncmd"
)

func main() {
	prepareDesktopEnv()

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
		pluginspersona.Plugin(),
		pluginssearch.Plugin(),
		pluginssessioncmd.Plugin(),
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

func prepareDesktopEnv() {
	path := mergePath(os.Getenv("PATH"), loginShellPath())
	if path != "" {
		_ = os.Setenv("PATH", path)
	}
}

func loginShellPath() string {
	shell := strings.TrimSpace(os.Getenv("SHELL"))
	if shell == "" {
		shell = "/bin/zsh"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	const mark = "__SUMI_PATH__"
	out, err := exec.CommandContext(ctx, shell, "-ilc", "printf '"+mark+"%s"+mark+"' \"$PATH\"").CombinedOutput()
	if err != nil {
		return ""
	}
	text := string(out)
	start := strings.Index(text, mark)
	if start < 0 {
		return ""
	}
	start += len(mark)
	end := strings.Index(text[start:], mark)
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(text[start : start+end])
}

func mergePath(paths ...string) string {
	seen := map[string]bool{}
	var out []string
	for _, path := range paths {
		for _, part := range strings.Split(path, string(os.PathListSeparator)) {
			part = strings.TrimSpace(part)
			if part == "" || seen[part] {
				continue
			}
			seen[part] = true
			out = append(out, part)
		}
	}
	return strings.Join(out, string(os.PathListSeparator))
}
