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
)

type Component string

const (
	Server   Component = "server"
	Computer Component = "computer"
)

type InstallConfig struct {
	Binary   string
	WebRoot  string
	DataRoot string
}

type Runner interface {
	Run(context.Context, string, ...string) error
}

type Manager struct {
	goos       string
	home       string
	uid        int
	runner     Runner
	labels     map[Component]string
	configHome string
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
	return &Manager{goos: goos, home: home, uid: uid, runner: runner, labels: map[Component]string{}, configHome: configHome}, nil
}

func (manager *Manager) Install(ctx context.Context, config InstallConfig) error {
	if err := validateInstallConfig(config); err != nil {
		return err
	}
	manager.setLabels(config.DataRoot)
	if manager.goos == "darwin" {
		return manager.installLaunchd(ctx, config)
	}
	return manager.installSystemd(ctx, config)
}

func (manager *Manager) Start(ctx context.Context, component Component) error {
	if err := manager.requireComponent(component); err != nil {
		return err
	}
	if manager.goos == "darwin" {
		return manager.startLaunchd(ctx, component)
	}
	return manager.runner.Run(ctx, "/usr/bin/systemctl", "--user", "start", manager.unitName(component))
}

func (manager *Manager) Stop(ctx context.Context, component Component) error {
	if err := manager.requireComponent(component); err != nil {
		return err
	}
	if manager.goos == "darwin" {
		return manager.stopLaunchd(ctx, component)
	}
	if !manager.Running(ctx, component) {
		return nil
	}
	return manager.runner.Run(ctx, "/usr/bin/systemctl", "--user", "stop", manager.unitName(component))
}

func (manager *Manager) Restart(ctx context.Context, component Component) error {
	if err := manager.requireComponent(component); err != nil {
		return err
	}
	if manager.goos == "darwin" {
		_ = manager.stopLaunchd(ctx, component)
		return manager.startLaunchd(ctx, component)
	}
	return manager.runner.Run(ctx, "/usr/bin/systemctl", "--user", "restart", manager.unitName(component))
}

func (manager *Manager) Running(ctx context.Context, component Component) bool {
	if manager.requireComponent(component) != nil {
		return false
	}
	if manager.goos == "darwin" {
		return manager.runner.Run(ctx, "/bin/launchctl", "print", manager.domainTarget(component)) == nil
	}
	return manager.runner.Run(ctx, "/usr/bin/systemctl", "--user", "is-active", "--quiet", manager.unitName(component)) == nil
}

func (manager *Manager) Uninstall(ctx context.Context) error {
	var failures []error
	for _, component := range []Component{Computer, Server} {
		if err := manager.Stop(ctx, component); err != nil {
			failures = append(failures, err)
		}
		if manager.goos == "linux" {
			if err := manager.runner.Run(ctx, "/usr/bin/systemctl", "--user", "disable", manager.unitName(component)); err != nil {
				failures = append(failures, err)
			}
		}
		if err := removeUnit(manager.unitPath(component)); err != nil {
			failures = append(failures, err)
		}
	}
	if manager.goos == "linux" {
		if err := manager.runner.Run(ctx, "/usr/bin/systemctl", "--user", "daemon-reload"); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (manager *Manager) Configure(dataRoot string) {
	manager.setLabels(dataRoot)
}

func (manager *Manager) UnitPaths() []string {
	return []string{manager.unitPath(Server), manager.unitPath(Computer)}
}

func (manager *Manager) setLabels(dataRoot string) {
	digest := sha256.Sum256([]byte(filepath.Clean(dataRoot)))
	suffix := hex.EncodeToString(digest[:6])
	manager.labels[Server] = "com.sumi.server." + suffix
	manager.labels[Computer] = "com.sumi.computer." + suffix
}

func (manager *Manager) requireComponent(component Component) error {
	if component != Server && component != Computer {
		return errors.New("service component is invalid")
	}
	if manager.labels[component] == "" {
		return errors.New("service data root is not configured")
	}
	return nil
}

func (manager *Manager) unitName(component Component) string {
	if manager.goos == "darwin" {
		return manager.labels[component] + ".plist"
	}
	return "sumi-" + string(component) + "-" + strings.TrimPrefix(manager.labels[component], "com.sumi."+string(component)+".") + ".service"
}

func (manager *Manager) unitPath(component Component) string {
	if manager.goos == "darwin" {
		return filepath.Join(manager.home, "Library", "LaunchAgents", manager.unitName(component))
	}
	return filepath.Join(manager.configHome, "systemd", "user", manager.unitName(component))
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
		return errors.New("current-user service command failed")
	}
	return nil
}
