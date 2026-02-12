package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/abcdlsj/mink/agent"
	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/cmd"
	"github.com/abcdlsj/mink/config"
	"github.com/abcdlsj/mink/hook"
	"github.com/abcdlsj/mink/llm"
	"github.com/abcdlsj/mink/platform"
	"github.com/abcdlsj/mink/session"
)

func main() {
	cfg := config.Load()

	// 检查运行模式：tg 或 cli（默认）
	var tgMode bool
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "tg" {
		tgMode = true
		os.Args = append([]string{os.Args[0]}, args[1:]...) // 移除 tg 参数
	}

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

	if tgMode && cfg.Telegram == "" {
		fmt.Fprintln(os.Stderr, "tg mode need telegram token")
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
	store := session.NewFileStore(dir)
	sm := session.NewManager(store, b)

	hooks := hook.NewManager()
	cmdReg := cmd.NewRegistry()
	cmdReg.Register(cmd.NewHelpCmd(cmdReg))
	cmdReg.Register(cmd.NewCompactCmd(b))
	router := cmd.NewRouter(cmdReg)
	guard := cmd.NewGuardMux()
	router.SetGuard(guard)

	sup := agent.NewSupervisor(b, p, sm, hooks, router, cfg.CustomPrompt)
	sup.SetConfig(cfg)
	disp := agent.NewDispatcher(b, sm, p)
	disp.SetAgentID(bus.AddrAgentMain)
	disp.SetHooks(hooks)
	disp.SetRouter(router)
	disp.SetPrompt(cfg.CustomPrompt)
	disp.SetConfig(cfg)
	disp.Start(ctx)
	cmdReg.Register(cmd.NewTokensCmd(func(src string) (cmd.TokenUsage, bool) {
		u, ok := disp.Usage(src)
		if !ok {
			return cmd.TokenUsage{}, false
		}
		return cmd.TokenUsage{
			Messages: u.Messages,
			Total:    u.Total,
			Input:    u.Input,
			Output:   u.Output,
			System:   u.System,
			Tool:     u.Tool,
			Source:   u.Source,
		}, true
	}))

	cmdReg.Register(cmd.NewSessionCmd(sm))

	var adapters []platform.Adapter

	if tgMode {
		// TG 模式：只启动 Telegram
		tg := platform.NewTelegram(cfg.Telegram, b)
		if err := tg.Start(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "error: telegram start failed: %v\n", err)
			os.Exit(1)
		}
		adapters = append(adapters, tg)
		guard.Register("telegram:", tg)
		fmt.Println("TG Bot started, press Ctrl+C to stop")

		// 等待退出信号
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		fmt.Println("\nShutting down...")
	} else {
		// CLI 模式
		cli := platform.NewCLI(b, router, hooks)
		cli.Start(ctx)
		adapters = append(adapters, cli)
		guard.Register(bus.AddrPlatformCLI, cli)

		if cfg.Telegram != "" {
			tg := platform.NewTelegram(cfg.Telegram, b)
			if err := tg.Start(ctx); err != nil {
				// telegram 错误不阻止启动
			} else {
				adapters = append(adapters, tg)
				guard.Register("telegram:", tg)
			}
		}

		if err := cli.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
		}
	}

	_ = sup

	cancel()
	for _, a := range adapters {
		a.Stop()
	}
}
