package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/abcdlsj/mink/config"
)

func runTG() {
	cfg := config.Load()

	fs := flag.NewFlagSet("tg", flag.ExitOnError)
	fs.StringVar(&cfg.Active.Provider, "p", cfg.Active.Provider, "provider")
	fs.StringVar(&cfg.Active.APIKey, "k", cfg.Active.APIKey, "api key")
	fs.StringVar(&cfg.Active.BaseURL, "u", cfg.Active.BaseURL, "base url")
	fs.StringVar(&cfg.Active.Model, "m", cfg.Active.Model, "model")
	fs.Parse(os.Args[2:])

	if cfg.Key("TELEGRAM_TOKEN") == "" {
		fail("tg mode need telegram token\n")
	}
	cfg.Mode = "tg"

	app := newApp(cfg)
	defer app.Close()

	ctx := context.Background()
	if err := app.StartTelegram(ctx, cfg.Key("TELEGRAM_TOKEN")); err != nil {
		fail("error: telegram start failed: %v\n", err)
	}
	fmt.Println("TG Bot started, press Ctrl+C to stop")

	waitForSignal()
}
