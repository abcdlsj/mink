package osservice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type Component string

const (
	Server   Component = "server"
	Computer Component = "computer"
)

var errLaunchdNotLoaded = errors.New("launchd service is not loaded")

type InstallConfig struct {
	Binary   string
	WebRoot  string
	DataRoot string
}

type Runner interface {
	Run(context.Context, string, ...string) error
}

type Manager struct {
	goos                       string
	home                       string
	uid                        int
	runner                     Runner
	labels                     map[Component]string
	configHome                 string
	now                        func() time.Time
	wait                       func(context.Context, time.Duration) error
	launchdRemovalTimeout      time.Duration
	launchdRemovalPollInterval time.Duration
}

func New() (*Manager, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, errors.New("resolve user home")
	}
	return NewManager(runtime.GOOS, home, os.Geteuid(), commandRunner{})
}

func NewManager(goos, home string, uid int, runner Runner) (*Manager, error) {
	if uid == 0 {
		return nil, errors.New("system service scope is not supported")
	}
	if goos != "darwin" && goos != "linux" {
		return nil, errors.New("current-user services are unsupported on this operating system")
	}
	if !filepath.IsAbs(home) || runner == nil {
		return nil, errors.New("current-user service configuration is invalid")
	}
	configHome := ""
	if goos == "linux" {
		configHome = os.Getenv("XDG_CONFIG_HOME")
		if configHome == "" {
			configHome = filepath.Join(home, ".config")
		}
		if !filepath.IsAbs(configHome) {
			return nil, errors.New("xdg config home must be absolute")
		}
	}
	return &Manager{
		goos: goos, home: home, uid: uid, runner: runner, labels: map[Component]string{}, configHome: configHome,
		now: time.Now, wait: waitForContext,
		launchdRemovalTimeout: 10 * time.Second, launchdRemovalPollInterval: 25 * time.Millisecond,
	}, nil
}

func (m *Manager) Install(ctx context.Context, config InstallConfig) error {
	if err := validateInstallConfig(config); err != nil {
		return err
	}
	m.setLabels(config.DataRoot)
	if m.goos == "darwin" {
		return m.installLaunchd(ctx, config)
	}
	return m.installSystemd(ctx, config)
}

func (m *Manager) Start(ctx context.Context, component Component) error {
	if err := m.requireComponent(component); err != nil {
		return err
	}
	if m.goos == "darwin" {
		return m.startLaunchd(ctx, component)
	}
	return m.runner.Run(ctx, "/usr/bin/systemctl", "--user", "start", m.unitName(component))
}

func (m *Manager) Stop(ctx context.Context, component Component) error {
	if err := m.requireComponent(component); err != nil {
		return err
	}
	if m.goos == "darwin" {
		return m.stopLaunchd(ctx, component)
	}
	if !m.Running(ctx, component) {
		return nil
	}
	return m.runner.Run(ctx, "/usr/bin/systemctl", "--user", "stop", m.unitName(component))
}

func (m *Manager) Restart(ctx context.Context, component Component) error {
	if err := m.requireComponent(component); err != nil {
		return err
	}
	if m.goos == "darwin" {
		if err := m.stopLaunchd(ctx, component); err != nil {
			return err
		}
		if err := m.waitForLaunchdRemoval(ctx, component); err != nil {
			return err
		}
		return m.startLaunchd(ctx, component)
	}
	return m.runner.Run(ctx, "/usr/bin/systemctl", "--user", "restart", m.unitName(component))
}

func (m *Manager) waitForLaunchdRemoval(ctx context.Context, component Component) error {
	deadline := m.now().Add(m.launchdRemovalTimeout)
	for {
		loaded, err := m.launchdLoaded(ctx, component)
		if err != nil {
			return err
		}
		if !loaded {
			return nil
		}
		remaining := deadline.Sub(m.now())
		if remaining <= 0 {
			return errors.New("current-user service removal timed out")
		}
		delay := min(m.launchdRemovalPollInterval, remaining)
		if err := m.wait(ctx, delay); err != nil {
			return err
		}
	}
}

func (m *Manager) Running(ctx context.Context, component Component) bool {
	if m.requireComponent(component) != nil {
		return false
	}
	if m.goos == "darwin" {
		loaded, err := m.launchdLoaded(ctx, component)
		return err == nil && loaded
	}
	return m.runner.Run(ctx, "/usr/bin/systemctl", "--user", "is-active", "--quiet", m.unitName(component)) == nil
}

func (m *Manager) launchdLoaded(ctx context.Context, component Component) (bool, error) {
	err := m.runner.Run(ctx, "/bin/launchctl", "print", m.domainTarget(component))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, errLaunchdNotLoaded) {
		return false, nil
	}
	return false, err
}

func (m *Manager) Uninstall(ctx context.Context) error {
	var failures []error
	for _, component := range []Component{Computer, Server} {
		if err := m.Stop(ctx, component); err != nil {
			failures = append(failures, err)
		}
		if m.goos == "linux" {
			if err := m.runner.Run(ctx, "/usr/bin/systemctl", "--user", "disable", m.unitName(component)); err != nil {
				failures = append(failures, err)
			}
		}
		if err := removeUnit(m.unitPath(component)); err != nil {
			failures = append(failures, err)
		}
	}
	if m.goos == "linux" {
		if err := m.runner.Run(ctx, "/usr/bin/systemctl", "--user", "daemon-reload"); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (m *Manager) Configure(dataRoot string) {
	m.setLabels(dataRoot)
}

func (m *Manager) UnitPaths() []string {
	return []string{m.unitPath(Server), m.unitPath(Computer)}
}

func (m *Manager) setLabels(dataRoot string) {
	digest := sha256.Sum256([]byte(filepath.Clean(dataRoot)))
	suffix := hex.EncodeToString(digest[:6])
	m.labels[Server] = "com.sumi.server." + suffix
	m.labels[Computer] = "com.sumi.computer." + suffix
}

func (m *Manager) requireComponent(component Component) error {
	if component != Server && component != Computer {
		return errors.New("service component is invalid")
	}
	if m.labels[component] == "" {
		return errors.New("service data root is not configured")
	}
	return nil
}

func (m *Manager) unitName(component Component) string {
	if m.goos == "darwin" {
		return m.labels[component] + ".plist"
	}
	return "sumi-" + string(component) + "-" + strings.TrimPrefix(m.labels[component], "com.sumi."+string(component)+".") + ".service"
}

func (m *Manager) unitPath(component Component) string {
	if m.goos == "darwin" {
		return filepath.Join(m.home, "Library", "LaunchAgents", m.unitName(component))
	}
	return filepath.Join(m.configHome, "systemd", "user", m.unitName(component))
}

func validateInstallConfig(config InstallConfig) error {
	for _, path := range []string{config.Binary, config.WebRoot, config.DataRoot} {
		if !filepath.IsAbs(path) || strings.ContainsRune(path, 0) {
			return errors.New("service path is invalid")
		}
	}
	return nil
}

func ensureUnitDirectory(path string) error {
	info, err := os.Lstat(path)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("service unit directory is unsafe")
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return errors.New("inspect service unit directory")
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return errors.New("create service unit directory")
	}
	return nil
}

func writeUnit(path string, payload []byte) error {
	if err := ensureUnitDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil && (info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular()) {
		return errors.New("service unit is unsafe")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.New("inspect service unit")
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".sumi-unit-*.tmp")
	if err != nil {
		return errors.New("create temporary service unit")
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(payload); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return errors.New("publish service unit")
	}
	return nil
}

func removeUnit(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("service unit is unsafe")
	}
	return os.Remove(path)
}

type commandRunner struct{}

func (commandRunner) Run(ctx context.Context, executable string, args ...string) error {
	command := exec.CommandContext(ctx, executable, args...)
	command.Env = os.Environ()
	if err := command.Run(); err != nil {
		var exitError *exec.ExitError
		if executable == "/bin/launchctl" && len(args) > 0 && args[0] == "print" &&
			errors.As(err, &exitError) && exitError.ExitCode() == 113 {
			return errLaunchdNotLoaded
		}
		return errors.New("current-user service command failed")
	}
	return nil
}

func waitForContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
