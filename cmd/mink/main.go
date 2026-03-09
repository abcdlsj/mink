package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/abcdlsj/mink"
	"github.com/abcdlsj/mink/config"
	"github.com/abcdlsj/mink/internal/updater"
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
	case "devbuild":
		runDevBuild()
	case "upgrade":
		runUpgrade()
	case "update":
		runUpgrade()
	case "version":
		runVersion()
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

func runDevBuild() {
	repoDir, err := detectModuleRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: detect repo dir: %v\n", err)
		os.Exit(1)
	}
	if err := buildLocalBinary(repoDir); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("local build installed")

	if err := sendDaemonUpgradeSignal(); err != nil {
		fmt.Println("daemon: not running, binary updated only")
		return
	}
	fmt.Println("daemon handoff triggered")
}

func runUpgrade() {
	version := mink.Version
	if version == "" {
		version = "dev"
	}

	u := updater.New(version)
	if err := u.Update(); err != nil {
		if errors.Is(err, updater.ErrAlreadyLatest) {
			fmt.Println(err.Error())
			return
		}
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if err := sendDaemonUpgradeSignal(); err != nil {
		fmt.Println("daemon: not running, binary updated only")
		return
	}
	fmt.Println("daemon handoff triggered")
}

func runVersion() {
	fmt.Printf("mink version %s\n", mink.Version)
	fmt.Printf("  commit: %s\n", mink.Commit)
	fmt.Printf("  built:  %s\n", mink.BuildTime)
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
	return mink.DefaultSockPath()
}

func daemonPID() (int, error) {
	pidfile := mink.DefaultPIDPath()
	data, err := os.ReadFile(pidfile)
	if err != nil {
		return 0, err
	}
	var pid int
	_, err = fmt.Sscanf(string(data), "%d", &pid)
	return pid, err
}

func sendDaemonUpgradeSignal() error {
	pid, err := daemonPID()
	if err != nil {
		return err
	}
	return syscall.Kill(pid, syscall.SIGUSR2)
}

func buildLocalBinary(repoDir string) error {
	cmd := exec.Command("go", "install", "./cmd/mink/")
	cmd.Dir = repoDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func detectModuleRoot() (string, error) {
	if _, err := os.Stat("go.mod"); err == nil {
		return filepath.Abs(".")
	}
	cmd := exec.Command("go", "list", "-m", "-f", "{{.Dir}}")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("no go.mod in cwd and go list failed: %w", err)
	}
	dir := strings.TrimSpace(string(out))
	if dir == "" {
		return "", fmt.Errorf("empty module root")
	}
	return dir, nil
}
