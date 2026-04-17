package main

import (
	"context"
	"fmt"
	"os"

	"github.com/abcdlsj/mink/app"
	"github.com/abcdlsj/mink/config"
	pluginsclaude "github.com/abcdlsj/mink/plugins/claude"
	pluginscodex "github.com/abcdlsj/mink/plugins/codex"
)

func main() {
	cfg := config.Load()
	a, err := app.New(cfg)
	if err != nil {
		fail(err)
	}
	defer a.Close()
	if err := a.Use(
		pluginsclaude.Plugin(),
		pluginscodex.Plugin(),
	); err != nil {
		fail(err)
	}
	if err := a.Run(context.Background(), os.Args[1:]); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
