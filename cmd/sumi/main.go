package main

import (
	"context"
	"encoding/json"
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
	"github.com/abcdlsj/sumi/store"
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
		case "spaces":
			runSpacesDump(os.Args[2:])
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

func runSpacesDump(args []string) {
	cfg := config.Load()
	st, err := store.Open(cfg.DataRoot())
	if err != nil {
		fail(err)
	}
	if len(args) == 0 {
		spaces, err := st.ListSpaces()
		if err != nil {
			fail(err)
		}
		out := make([]map[string]any, 0, len(spaces))
		for _, sp := range spaces {
			out = append(out, map[string]any{
				"id":           sp.ID,
				"kind":         sp.Kind,
				"title":        sp.Title,
				"participants": len(sp.Participants),
				"messages":     len(sp.Messages),
				"updated_at":   sp.UpdatedAt,
			})
		}
		printJSON(out)
		return
	}
	sp, err := st.LoadSpace(args[0])
	if err != nil {
		fail(err)
	}
	printJSON(sp)
}

func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fail(err)
	}
}
