package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/abcdlsj/mink/config"
)

func runWeb() {
	cfg := config.Load()

	fs := flag.NewFlagSet("web", flag.ExitOnError)
	fs.StringVar(&cfg.Active.Provider, "p", cfg.Active.Provider, "provider")
	fs.StringVar(&cfg.Active.APIKey, "k", cfg.Active.APIKey, "api key")
	fs.StringVar(&cfg.Active.BaseURL, "u", cfg.Active.BaseURL, "base url")
	fs.StringVar(&cfg.Active.Model, "m", cfg.Active.Model, "model")
	fs.StringVar(&cfg.WebAddr, "addr", cfg.WebAddr, "web bind address")
	fs.Parse(os.Args[2:])

	app := newApp(cfg)
	defer app.Close()

	ctx := context.Background()
	if err := app.StartWeb(ctx, cfg.WebAddr); err != nil {
		fail("error: web start failed: %v\n", err)
	}

	waitForSignal()
}

func waitForSignal() {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	fmt.Println("\nShutting down...")
}
