package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/abcdlsj/mink/agent"
	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/cmd"
	"github.com/abcdlsj/mink/config"
	"github.com/abcdlsj/mink/hook"
	"github.com/abcdlsj/mink/llm"
	"github.com/abcdlsj/mink/platform"
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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

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
	hooks := hook.NewManager()
	cmdReg := cmd.NewRegistry()
	cmdReg.Register(cmd.NewHelpCmd(cmdReg))
	router := cmd.NewRouter(cmdReg)
	guard := cmd.NewGuardMux()

	sup := agent.NewSupervisor(b, p, dir, hooks, router, cfg.CustomPrompt)
	a := agent.New("main", p, dir, b, hooks, router, cfg.CustomPrompt)
	sup.Register(a)

	cmdReg.Register(cmd.NewToolsCmd(a.Tools()))
	cmdReg.Register(cmd.NewSessionCmd(a.Sessions()))

	a.Start(ctx)

	var adapters []platform.Adapter

	cli := platform.NewCLI(b, router, hooks)
	cli.Start(ctx)
	adapters = append(adapters, cli)
	guard.Register("cli", cli)

	if cfg.Telegram != "" {
		tg := platform.NewTelegram(cfg.Telegram, b)
		if err := tg.Start(ctx); err != nil {
			// telegram 错误不阻止启动，会在状态栏显示
		} else {
			adapters = append(adapters, tg)
			guard.Register("telegram:", tg)
		}
	}

	router.SetGuard(guard)

	// TUI 在主线程运行（阻塞直到退出）
	if err := cli.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
	}

	cancel()
	for _, a := range adapters {
		a.Stop()
	}
}
