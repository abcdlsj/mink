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
	if len(os.Args) < 2 {
		runCLI()
		return
	}

	switch os.Args[1] {
	case "version":
		runVersion()
	case "web":
		runWeb()
	case "tg":
		runTG()
	default:
		os.Args = append([]string{os.Args[0]}, os.Args[1:]...)
		runCLI()
	}
}

func runCLI() {
	cfg := config.Load()

	flag.StringVar(&cfg.Active.Provider, "p", cfg.Active.Provider, "provider")
	flag.StringVar(&cfg.Active.APIKey, "k", cfg.Active.APIKey, "api key")
	flag.StringVar(&cfg.Active.BaseURL, "u", cfg.Active.BaseURL, "base url")
	flag.StringVar(&cfg.Active.Model, "m", cfg.Active.Model, "model")
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
	if err := app.StartCLI(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if err := app.RunCLI(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
	}
}

func runVersion() {
	fmt.Printf("mink version %s\n", mink.Version)
	fmt.Printf("  commit: %s\n", mink.Commit)
	fmt.Printf("  built:  %s\n", mink.BuildTime)
}

func runTG() {
	cfg := config.Load()

	fs := flag.NewFlagSet("tg", flag.ExitOnError)
	fs.StringVar(&cfg.Active.Provider, "p", cfg.Active.Provider, "provider")
	fs.StringVar(&cfg.Active.APIKey, "k", cfg.Active.APIKey, "api key")
	fs.StringVar(&cfg.Active.BaseURL, "u", cfg.Active.BaseURL, "base url")
	fs.StringVar(&cfg.Active.Model, "m", cfg.Active.Model, "model")
	fs.Parse(os.Args[2:])

	if cfg.Key("TELEGRAM_TOKEN") == "" {
		fmt.Fprintln(os.Stderr, "tg mode need telegram token")
		os.Exit(1)
	}
	cfg.Mode = "tg"

	app, err := mink.New(mink.Options{Config: cfg})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer app.Close()

	ctx := context.Background()
	if err := app.StartTelegram(ctx, cfg.Key("TELEGRAM_TOKEN")); err != nil {
		fmt.Fprintf(os.Stderr, "error: telegram start failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("TG Bot started, press Ctrl+C to stop")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	fmt.Println("\nShutting down...")
}

func runWeb() {
	cfg := config.Load()

	fs := flag.NewFlagSet("web", flag.ExitOnError)
	fs.StringVar(&cfg.Active.Provider, "p", cfg.Active.Provider, "provider")
	fs.StringVar(&cfg.Active.APIKey, "k", cfg.Active.APIKey, "api key")
	fs.StringVar(&cfg.Active.BaseURL, "u", cfg.Active.BaseURL, "base url")
	fs.StringVar(&cfg.Active.Model, "m", cfg.Active.Model, "model")
	fs.StringVar(&cfg.WebAddr, "addr", cfg.WebAddr, "web bind address")
	fs.Parse(os.Args[2:])

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
	if err := app.StartWeb(ctx, cfg.WebAddr); err != nil {
		fmt.Fprintf(os.Stderr, "error: web start failed: %v\n", err)
		os.Exit(1)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	fmt.Println("\nShutting down...")
}
