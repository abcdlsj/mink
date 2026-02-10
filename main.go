package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/charmbracelet/lipgloss"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/config"
	"github.com/abcdlsj/mink/core"
	"github.com/abcdlsj/mink/llm"
	"github.com/abcdlsj/mink/telegram"
)

var (
	cyan   = lipgloss.NewStyle().Foreground(lipgloss.Color("#00FFFF"))
	green  = lipgloss.NewStyle().Foreground(lipgloss.Color("#00FF00"))
	yellow = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFF00"))
	red    = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF0000"))
	gray   = lipgloss.NewStyle().Foreground(lipgloss.Color("#888888"))
)

func main() {
	cfg := config.Load()

	flag.StringVar(&cfg.Provider, "p", cfg.Provider, "provider")
	flag.StringVar(&cfg.APIKey, "k", cfg.APIKey, "api key")
	flag.StringVar(&cfg.BaseURL, "u", cfg.BaseURL, "base url")
	flag.StringVar(&cfg.Model, "m", cfg.Model, "model")
	flag.StringVar(&cfg.Telegram, "tg", cfg.Telegram, "telegram token")
	flag.Parse()

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
	b.Subscribe(bus.TypeToolCall, ch)
	b.Subscribe(bus.TypeToolResult, ch)
	b.Subscribe(bus.TypeToolError, ch)

	fmt.Println(gray.Render("mink. type 'exit' to quit"))

	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Print(cyan.Render("> "))
		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}

		in := strings.TrimSpace(line)
		if in == "" {
			continue
		}
		if in == "exit" {
			break
		}

		b.Pub(bus.Msg{
			Type:    bus.TypeUserInput,
			From:    "cli",
			To:      "main",
			Payload: in,
		})

		done := false
		for !done {
			m := <-ch
			if m.To != "" && m.To != "cli" && m.To != "*" {
				continue
			}
			switch m.Type {
			case bus.TypeAssistant:
				fmt.Printf("%s %s\n", green.Render("🤖"), green.Render(fmt.Sprintf("%v", m.Payload)))
				done = true
			case bus.TypeToolCall:
				fmt.Printf("%s %s\n", yellow.Render("◉"), yellow.Render(fmt.Sprintf("%v", m.Payload)))
			case bus.TypeToolResult:
				out := fmt.Sprintf("%v", m.Payload)
				lines := strings.Split(out, "\n")
				fmt.Printf("%s result:\n", green.Render("✓"))
				for i, line := range lines {
					if i >= 5 {
						fmt.Printf("  %s\n", gray.Render("... (truncated)"))
						break
					}
					if len(line) > 100 {
						fmt.Printf("  %s...\n", gray.Render(line[:100]))
					} else {
						fmt.Printf("  %s\n", gray.Render(line))
					}
				}
			case bus.TypeToolError:
				fmt.Printf("%s %s\n", red.Render("✗"), red.Render(fmt.Sprintf("%v", m.Payload)))
			}
		}
	}
}
