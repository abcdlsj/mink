package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/abcdlsj/mink"
	"github.com/abcdlsj/mink/config"
)

func main() {
	cfg := config.Load()

	var tgMode bool
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "tg" {
		tgMode = true
		os.Args = append([]string{os.Args[0]}, args[1:]...)
	}

	flag.StringVar(&cfg.Provider, "p", cfg.Provider, "provider")
	flag.StringVar(&cfg.APIKey, "k", cfg.APIKey, "api key")
	flag.StringVar(&cfg.BaseURL, "u", cfg.BaseURL, "base url")
	flag.StringVar(&cfg.Model, "m", cfg.Model, "model")
	flag.StringVar(&cfg.Telegram, "tg_token", cfg.Telegram, "telegram token")
	flag.Parse()

	app, err := mink.New(mink.Options{Config: cfg})
	if err != nil {
		if err == mink.ErrAPIKeyRequired {
			fmt.Fprintln(os.Stderr, "need api key")
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer app.Close()

	ctx := context.Background()
	if tgMode {
		if cfg.Telegram == "" {
			fmt.Fprintln(os.Stderr, "tg mode need telegram token")
			os.Exit(1)
		}
		if err := app.StartTelegram(ctx, cfg.Telegram); err != nil {
			fmt.Fprintf(os.Stderr, "error: telegram start failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("TG Bot started, press Ctrl+C to stop")

		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		fmt.Println("\nShutting down...")
		return
	}

	// CLI mode
	if err := app.StartCLI(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if err := app.RunCLI(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
	}
}
