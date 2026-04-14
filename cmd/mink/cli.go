package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/abcdlsj/mink/config"
)

func runCLI() {
	cfg := config.Load()

	flag.StringVar(&cfg.Active.Provider, "p", cfg.Active.Provider, "provider")
	flag.StringVar(&cfg.Active.APIKey, "k", cfg.Active.APIKey, "api key")
	flag.StringVar(&cfg.Active.BaseURL, "u", cfg.Active.BaseURL, "base url")
	flag.StringVar(&cfg.Active.Model, "m", cfg.Active.Model, "model")
	flag.Parse()

	app := newApp(cfg)
	defer app.Close()

	ctx := context.Background()
	if err := app.StartCLI(ctx); err != nil {
		fail("error: %v\n", err)
	}
	if err := app.RunCLI(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
	}
}
