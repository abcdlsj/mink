package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/abcdlsj/mink/agent"
	"github.com/abcdlsj/mink/config"
	"github.com/abcdlsj/mink/event"
	"github.com/abcdlsj/mink/llm"
	"github.com/abcdlsj/mink/telegram"
	"github.com/abcdlsj/mink/tui"
)

func main() {
	// load config first
	cfg := config.Load()

	// flags override config
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
		fmt.Fprintln(os.Stderr, "need api key. set in config or env")
		os.Exit(1)
	}

	bus := event.NewBus()

	lc := llm.Config{
		Provider: cfg.Provider,
		APIKey:   cfg.APIKey,
		BaseURL:  cfg.BaseURL,
		Model:    cfg.Model,
		Headers:  cfg.Headers,
	}

	p, err := llm.NewProvider(lc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	dir := os.ExpandEnv("$HOME/.mink/sessions")
	a := agent.New(p, dir, bus)

	ext := agent.NewExtManager()
	ext.LoadDir(os.ExpandEnv("$HOME/.mink/ext"))
	ext.LoadDir(os.ExpandEnv("$HOME/.mink/skills"))
	ext.Watch()
	ext.OnReload(func() { fmt.Println("[ext reloaded]") })
	a.SetExt(ext)

	if _, err := a.NewSession(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if cfg.Telegram != "" {
		bot := telegram.New(a, bus, cfg.Telegram)
		bot.Start()
		fmt.Println("telegram: ok")
	}

	if cfg.Mode == "cli" {
		runCLI(a, bus)
	} else {
		runTUI(a, bus)
	}
}

func runTUI(a *agent.Agent, b *event.Bus) {
	m := tui.New(a, b)
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func runCLI(a *agent.Agent, b *event.Bus) {
	b.Subscribe(event.AssistantMsg, func(e event.Event) {
		fmt.Printf("\n🤖 %s\n", e.Data)
	})
	b.Subscribe(event.ToolStart, func(e event.Event) {
		fmt.Printf("\n🔧 %s\n", e.Data.(map[string]string)["name"])
	})

	fmt.Println("mink. type 'exit'")
	for {
		fmt.Print("> ")
		var in string
		fmt.Scanln(&in)
		if in == "exit" {
			break
		}
		if err := a.Run(context.Background(), in); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
		}
	}
}
