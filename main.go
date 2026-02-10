package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/abcdlsj/mink/agent"
	"github.com/abcdlsj/mink/event"
	"github.com/abcdlsj/mink/llm"
	"github.com/abcdlsj/mink/telegram"
	"github.com/abcdlsj/mink/tui"
)

func main() {
	var (
		provider = flag.String("p", "openai", "provider")
		key      = flag.String("k", "", "api key")
		url      = flag.String("u", "", "base url")
		model    = flag.String("m", "gpt-4o", "model")
		tgToken  = flag.String("tg", "", "telegram token")
		cliMode  = flag.Bool("c", false, "cli mode")
	)
	flag.Parse()

	bus := event.NewBus()

	cfg := llm.Config{
		Provider: *provider,
		APIKey:   *key,
		BaseURL:  *url,
		Model:    *model,
		Headers:  make(map[string]string),
	}

	if cfg.APIKey == "" {
		switch *provider {
		case "openai":
			cfg.APIKey = os.Getenv("OPENAI_API_KEY")
		case "anthropic":
			cfg.APIKey = os.Getenv("ANTHROPIC_API_KEY")
		}
	}

	if cfg.APIKey == "" {
		fmt.Fprintln(os.Stderr, "need api key")
		os.Exit(1)
	}

	p, err := llm.NewProvider(cfg)
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

	if *tgToken != "" {
		bot := telegram.New(a, bus, *tgToken)
		bot.Start()
		fmt.Println("telegram: ok")
	}

	if *cliMode {
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
