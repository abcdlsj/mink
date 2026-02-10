package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/config"
	"github.com/abcdlsj/mink/core"
	"github.com/abcdlsj/mink/llm"
	"github.com/abcdlsj/mink/telegram"
)

func main() {
	cfg := config.Load()

	flag.StringVar(&cfg.Provider, "p", cfg.Provider, "provider")
	flag.StringVar(&cfg.APIKey, "k", cfg.APIKey, "api key")
	flag.StringVar(&cfg.BaseURL, "u", cfg.BaseURL, "base url")
	flag.StringVar(&cfg.Model, "m", cfg.Model, "model")
	flag.StringVar(&cfg.Telegram, "tg", cfg.Telegram, "telegram token")
	cli := flag.Bool("c", cfg.Mode == "cli", "cli mode")
	flag.Parse()

	if *cli {
		cfg.Mode = "cli"
	}

	if cfg.APIKey == "" {
		fmt.Fprintln(os.Stderr, "need api key")
		os.Exit(1)
	}

	b := bus.New()

	p, err := llm.NewProvider(llm.Config{
		Provider: cfg.Provider,
		APIKey:   cfg.APIKey,
		BaseURL:  cfg.BaseURL,
		Model:    cfg.Model,
		Headers:  cfg.Headers,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	dir := os.ExpandEnv("$HOME/.mink/sessions")
	agent := core.New("main", p, dir, b)
	agent.Start(context.Background())

	if cfg.Telegram != "" {
		bot := telegram.New(cfg.Telegram, b)
		if err := bot.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "telegram error: %v\n", err)
		} else {
			fmt.Println("telegram: ok")
		}
		defer bot.Stop()
	}

	runCLI(b)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
}

func runCLI(b *bus.Bus) {
	ch := make(chan bus.Msg, 64)
	b.Subscribe(bus.TypeAssistant, ch)

	go func() {
		for m := range ch {
			if m.To != "" && m.To != "cli" && m.To != "*" {
				continue
			}
			fmt.Printf("\n🤖 %s\n", m.Payload)
			fmt.Print("> ")
		}
	}()

	fmt.Println("mink. type 'exit'")
	fmt.Print("> ")

	for {
		var in string
		fmt.Scanln(&in)
		if in == "exit" {
			break
		}

		b.Pub(bus.Msg{
			Type:    bus.TypeUserInput,
			From:    "cli",
			To:      "main",
			Payload: in,
		})
	}
}
