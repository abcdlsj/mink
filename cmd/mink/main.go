package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
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
	case "serve":
		runServe()
	case "reload":
		runReload()
	case "upgrade":
		runUpgrade()
	case "status":
		runStatus()
	case "tg":
		runTG()
	default:
		os.Args = append([]string{os.Args[0]}, os.Args[1:]...)
		runCLI()
	}
}

func runCLI() {
	cfg := config.Load()

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
	if err := app.StartCLI(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if err := app.RunCLI(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
	}
}

func runServe() {
	var inheritFd int
	var sock string
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	fs.IntVar(&inheritFd, "inherit-fd", 0, "inherited file descriptor")
	fs.StringVar(&sock, "sock", "", "socket path")
	fs.Parse(os.Args[2:])

	cfg := config.Load()
	app, err := mink.New(mink.Options{Config: cfg})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	d := mink.NewDaemon(app, sock)

	if inheritFd > 0 {
		f := os.NewFile(uintptr(inheritFd), "listener")
		ln, err := net.FileListener(f)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: inherit listener: %v\n", err)
			os.Exit(1)
		}
		d.SetListener(ln)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	if err := d.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func runReload() {
	if err := sendCmd("reload"); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("reload signal sent")
}

func runUpgrade() {
	pid, err := daemonPID()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: daemon not running\n")
		os.Exit(1)
	}
	if err := syscall.Kill(pid, syscall.SIGUSR2); err != nil {
		fmt.Fprintf(os.Stderr, "error: send upgrade signal: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("upgrade signal sent")
}

func runStatus() {
	resp, err := sendCmdWithResp("ping")
	if err != nil {
		fmt.Println("daemon: not running")
		os.Exit(1)
	}
	if resp.OK {
		fmt.Println("daemon: running")
	} else {
		fmt.Println("daemon: error")
	}
}

func runTG() {
	cfg := config.Load()

	fs := flag.NewFlagSet("tg", flag.ExitOnError)
	fs.StringVar(&cfg.Provider, "p", cfg.Provider, "provider")
	fs.StringVar(&cfg.APIKey, "k", cfg.APIKey, "api key")
	fs.StringVar(&cfg.BaseURL, "u", cfg.BaseURL, "base url")
	fs.StringVar(&cfg.Model, "m", cfg.Model, "model")
	fs.StringVar(&cfg.Telegram, "tg_token", cfg.Telegram, "telegram token")
	fs.Parse(os.Args[2:])

	if cfg.Telegram == "" {
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
	if err := app.StartTelegram(ctx, cfg.Telegram); err != nil {
		fmt.Fprintf(os.Stderr, "error: telegram start failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("TG Bot started, press Ctrl+C to stop")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	fmt.Println("\nShutting down...")
}

func sendCmd(cmd string) error {
	_, err := sendCmdWithResp(cmd)
	return err
}

func sendCmdWithResp(cmd string) (*struct {
	OK   bool   `json:"ok"`
	Data string `json:"data"`
}, error) {
	sock := defaultSockPath()
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	req := struct {
		Cmd string `json:"cmd"`
	}{Cmd: cmd}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return nil, err
	}

	var resp struct {
		OK   bool   `json:"ok"`
		Data string `json:"data"`
	}
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func defaultSockPath() string {
	home, _ := os.UserHomeDir()
	return home + "/.mink/mink.sock"
}

func daemonPID() (int, error) {
	home, _ := os.UserHomeDir()
	pidfile := filepath.Join(home, ".mink", "mink.pid")
	data, err := os.ReadFile(pidfile)
	if err != nil {
		return 0, err
	}
	var pid int
	_, err = fmt.Sscanf(string(data), "%d", &pid)
	return pid, err
}
