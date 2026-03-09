package mink

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/abcdlsj/mink/config"
	"github.com/abcdlsj/mink/internal/logx"
	"github.com/abcdlsj/mink/tool"
)

const (
	daemonDialTimeout         = 200 * time.Millisecond
	daemonUpgradeReadyTimeout = 3 * time.Second
	daemonUpgradeCheckIntv    = 100 * time.Millisecond
)

type daemonRequest struct {
	Cmd     string `json:"cmd"`
	Source  string `json:"source"`
	Payload string `json:"payload"`
}

type daemonResponse struct {
	OK    bool   `json:"ok"`
	Data  string `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
}

type Daemon struct {
	*App
	ln     net.Listener
	sock   string
	pid    string
	logger *logx.Logger

	mu        sync.RWMutex
	ctx       context.Context
	cancel    context.CancelFunc
	closed    bool
	upgrading bool
}

func NewDaemon(a *App, sock string) *Daemon {
	if sock == "" {
		sock = DefaultSockPath()
	}
	return &Daemon{
		App:    a,
		sock:   sock,
		pid:    DefaultPIDPath(),
		logger: logx.New("daemon"),
	}
}

func (d *Daemon) SetListener(ln net.Listener) {
	d.ln = ln
}

func (d *Daemon) Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	d.mu.Lock()
	d.ctx = ctx
	d.cancel = cancel
	d.mu.Unlock()

	if d.ln == nil {
		if err := d.listen(); err != nil {
			return err
		}
	}
	defer d.close()

	if err := d.writePID(); err != nil {
		return err
	}
	if err := d.Start(ctx); err != nil {
		return err
	}
	if err := d.reconcilePlatforms(ctx, d.cfg); err != nil {
		return err
	}

	d.resumeFromUpgrade()

	go d.handleSignals(ctx)
	go d.serve()

	<-ctx.Done()
	return nil
}

func (d *Daemon) listen() error {
	if err := os.MkdirAll(filepath.Dir(d.sock), 0o755); err != nil {
		return fmt.Errorf("daemon: create socket dir: %w", err)
	}
	if err := prepareSocketPath(d.sock); err != nil {
		return err
	}

	ln, err := net.Listen("unix", d.sock)
	if err != nil {
		return fmt.Errorf("daemon: listen %s: %w", d.sock, err)
	}
	d.ln = ln
	return nil
}

func prepareSocketPath(sock string) error {
	info, err := os.Stat(sock)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("daemon: socket path is not a unix socket: %s", sock)
	}

	conn, err := net.DialTimeout("unix", sock, daemonDialTimeout)
	if err == nil {
		_ = conn.Close()
		return fmt.Errorf("daemon already running: %s", sock)
	}
	if !errors.Is(err, syscall.ECONNREFUSED) && !errors.Is(err, syscall.ENOENT) {
		return fmt.Errorf("daemon: probe socket %s: %w", sock, err)
	}
	if remErr := os.Remove(sock); remErr != nil && !os.IsNotExist(remErr) {
		return fmt.Errorf("daemon: remove stale socket %s: %w", sock, remErr)
	}
	return nil
}

func (d *Daemon) writePID() error {
	if err := os.MkdirAll(filepath.Dir(d.pid), 0o755); err != nil {
		return fmt.Errorf("daemon: create pid dir: %w", err)
	}
	tmp := d.pid + ".tmp"
	if err := os.WriteFile(tmp, []byte(fmt.Sprintf("%d", os.Getpid())), 0o644); err != nil {
		return fmt.Errorf("daemon: write pid temp file: %w", err)
	}
	if err := os.Rename(tmp, d.pid); err != nil {
		return fmt.Errorf("daemon: move pid file: %w", err)
	}
	return nil
}

func (d *Daemon) close() {
	d.closeWithMode(false, false)
}

func (d *Daemon) closeWithMode(skipListener, keepRuntimeFiles bool) {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return
	}
	d.closed = true
	ln := d.ln
	sock := d.sock
	pid := d.pid
	d.mu.Unlock()

	if ln != nil && !skipListener {
		_ = ln.Close()
	}
	if !keepRuntimeFiles {
		_ = os.Remove(sock)
		_ = os.Remove(pid)
	}
	_ = d.App.Close()
}

func (d *Daemon) handleSignals(ctx context.Context) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGHUP, syscall.SIGUSR2, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(ch)

	for {
		select {
		case sig := <-ch:
			switch sig {
			case syscall.SIGHUP:
				if err := d.reload(ctx); err != nil {
					d.logger.Errorf("reload failed: %v", err)
				}
			case syscall.SIGUSR2:
				if err := d.upgrade(); err != nil {
					d.logger.Errorf("upgrade failed: %v", err)
				}
			case syscall.SIGINT, syscall.SIGTERM:
				d.shutdown()
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

func (d *Daemon) shutdown() {
	d.mu.RLock()
	cancel := d.cancel
	d.mu.RUnlock()
	if cancel != nil {
		cancel()
	}
}

func (d *Daemon) reconcilePlatforms(ctx context.Context, cfg config.Config) error {
	token := cfg.Key("TELEGRAM_TOKEN")
	if token == "" {
		return d.StopTelegram()
	}
	return d.StartTelegram(ctx, token)
}

func (d *Daemon) reload(ctx context.Context) error {
	cfg := config.Load()
	d.ReloadConfig(cfg)
	return d.reconcilePlatforms(ctx, cfg)
}

func (d *Daemon) upgrade() error {
	bin, err := os.Executable()
	if err != nil {
		return err
	}
	bin, err = filepath.EvalSymlinks(bin)
	if err != nil {
		return err
	}

	uln, ok := d.ln.(*net.UnixListener)
	if !ok {
		return fmt.Errorf("daemon upgrade requires a unix listener")
	}
	fd, err := uln.File()
	if err != nil {
		return err
	}
	defer fd.Close()

	cmd := exec.Command(bin, "serve", "--inherit-fd=3", "--sock="+d.sock)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.ExtraFiles = []*os.File{fd}

	if err := cmd.Start(); err != nil {
		return err
	}
	if err := d.waitForUpgradeReady(cmd.Process.Pid); err != nil {
		return err
	}

	d.mu.Lock()
	d.upgrading = true
	d.mu.Unlock()

	d.closeWithMode(true, true)
	os.Exit(0)
	return nil
}

func (d *Daemon) waitForUpgradeReady(childPID int) error {
	deadline := time.Now().Add(daemonUpgradeReadyTimeout)
	for time.Now().Before(deadline) {
		pid, err := readPIDFile(d.pid)
		if err == nil && pid == childPID {
			time.Sleep(daemonUpgradeCheckIntv)
			if err := syscall.Kill(childPID, 0); err == nil {
				return nil
			}
		}
		time.Sleep(daemonUpgradeCheckIntv)
	}
	return fmt.Errorf("upgrade child did not become active within %s", daemonUpgradeReadyTimeout)
}

func readPIDFile(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	var pid int
	_, err = fmt.Sscanf(string(data), "%d", &pid)
	return pid, err
}

func (d *Daemon) serve() {
	for {
		conn, err := d.ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			d.mu.RLock()
			closed := d.closed
			d.mu.RUnlock()
			if closed {
				return
			}
			continue
		}
		go d.handle(conn)
	}
}

func (d *Daemon) handle(conn net.Conn) {
	defer conn.Close()

	var req daemonRequest
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		_ = json.NewEncoder(conn).Encode(daemonResponse{OK: false, Error: err.Error()})
		return
	}

	resp := daemonResponse{}
	switch req.Cmd {
	case "ping":
		resp.OK = true
		resp.Data = "running"
	case "reload":
		if err := d.reload(d.ctx); err != nil {
			resp.Error = err.Error()
			break
		}
		resp.OK = true
		resp.Data = "reloaded"
	case "status":
		resp.OK = true
		resp.Data = "running"
	case "submit":
		src := req.Source
		if src == "" {
			src = "cli"
		}
		if err := d.Submit(src, req.Payload); err != nil {
			resp.Error = err.Error()
			break
		}
		resp.OK = true
		resp.Data = "submitted"
	default:
		resp.Error = fmt.Sprintf("unknown daemon command: %s", req.Cmd)
	}
	if resp.Error == "" && !resp.OK {
		resp.OK = true
	}

	_ = json.NewEncoder(conn).Encode(resp)
}

func (d *Daemon) resumeFromUpgrade() {
	state, err := tool.LoadUpgradeState()
	if err != nil {
		return
	}
	tool.RemoveUpgradeState()

	d.SetResumeSessions(state.Sessions)

	prompt := state.ResumePrompt
	if prompt == "" {
		prompt = "Self-update completed."
	}
	for src := range state.Sessions {
		_ = d.Submit(src, prompt)
	}
}
