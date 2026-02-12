package mink

import (
	"context"
	"encoding/json"
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
)

type Daemon struct {
	*App
	ln     net.Listener
	sock   string
	pid    string
	mu     sync.RWMutex
	closed bool
}

func NewDaemon(a *App, sock string) *Daemon {
	if sock == "" {
		sock = defaultSockPath()
	}
	return &Daemon{App: a, sock: sock, pid: defaultPIDPath()}
}

func (d *Daemon) SetListener(ln net.Listener) {
	d.ln = ln
}

func (d *Daemon) Run(ctx context.Context) error {
	if d.ln == nil {
		if err := d.listen(); err != nil {
			return err
		}
	}
	defer d.close()
	d.writePID()

	if err := d.Start(ctx); err != nil {
		return err
	}

	go d.handleSignals()
	go d.serve()

	<-ctx.Done()
	return nil
}

func (d *Daemon) listen() error {
	_ = os.Remove(d.sock)
	ln, err := net.Listen("unix", d.sock)
	if err != nil {
		return fmt.Errorf("daemon: listen %s: %w", d.sock, err)
	}
	d.ln = ln
	return nil
}

func (d *Daemon) writePID() {
	os.WriteFile(d.pid, []byte(fmt.Sprintf("%d", os.Getpid())), 0644)
}

func (d *Daemon) close() {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return
	}
	d.closed = true
	d.mu.Unlock()

	if d.ln != nil {
		_ = d.ln.Close()
	}
	_ = os.Remove(d.sock)
	_ = os.Remove(d.pid)
	_ = d.App.Close()
}

func (d *Daemon) handleSignals() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGHUP, syscall.SIGUSR2, syscall.SIGINT, syscall.SIGTERM)

	for sig := range ch {
		switch sig {
		case syscall.SIGHUP:
			d.reload()
		case syscall.SIGUSR2:
			d.upgrade()
		case syscall.SIGINT, syscall.SIGTERM:
			d.close()
			os.Exit(0)
		}
	}
}

func (d *Daemon) reload() {
	d.ReloadConfig(config.Load())
}

func (d *Daemon) upgrade() {
	bin, err := os.Executable()
	if err != nil {
		return
	}

	bin, _ = filepath.EvalSymlinks(bin)
	fd, err := d.ln.(*net.UnixListener).File()
	if err != nil {
		return
	}
	defer fd.Close()

	cmd := exec.Command(bin, "serve", "--inherit-fd=3", "--sock="+d.sock)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.ExtraFiles = []*os.File{fd}

	if err := cmd.Start(); err != nil {
		return
	}

	time.Sleep(100 * time.Millisecond)
	d.close()
	os.Exit(0)
}

func (d *Daemon) serve() {
	for {
		conn, err := d.ln.Accept()
		if err != nil {
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

	var req struct {
		Cmd     string `json:"cmd"`
		Source  string `json:"source"`
		Payload string `json:"payload"`
	}
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		return
	}

	var resp struct {
		OK    bool   `json:"ok"`
		Data  string `json:"data"`
		Error string `json:"error,omitempty"`
	}

	switch req.Cmd {
	case "ping":
		resp.OK = true
	case "reload":
		d.reload()
		resp.OK = true
	case "status":
		resp.OK = true
		resp.Data = "running"
	case "submit":
		src := req.Source
		if src == "" {
			src = "cli"
		}
		if err := d.Submit(src, req.Payload); err != nil {
			resp.OK = false
			resp.Error = err.Error()
		} else {
			resp.OK = true
			resp.Data = "submitted"
		}
	}

	_ = json.NewEncoder(conn).Encode(resp)
}

func defaultSockPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".mink", "mink.sock")
}

func defaultPIDPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".mink", "mink.pid")
}
