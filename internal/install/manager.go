package install

import (
	"context"
	"errors"
	"os"
	"runtime"
	"time"

	"github.com/abcdlsj/sumi/internal/lifecycle"
	"github.com/abcdlsj/sumi/internal/observability"
	"github.com/abcdlsj/sumi/internal/osservice"
	"github.com/abcdlsj/sumi/internal/releasebundle"
)

var ErrRestoreUnproven = errors.New("restore could not be proven")

type ServiceManager interface {
	Configure(string)
	Install(context.Context, osservice.InstallConfig) error
	Start(context.Context, osservice.Component) error
	Stop(context.Context, osservice.Component) error
	Restart(context.Context, osservice.Component) error
	Running(context.Context, osservice.Component) bool
	Uninstall(context.Context) error
}

type Prober interface {
	Probe(context.Context, string, string, string, *lifecycle.Lease) error
}

type Manager struct {
	Layout   Layout
	Services ServiceManager
	Prober   Prober
	GOOS     string
	GOARCH   string
	Before   func(string) error
	Logger   *observability.Logger
}

func New(dataRoot string) (*Manager, error) {
	layout, err := Resolve(dataRoot)
	if err != nil {
		return nil, err
	}
	services, err := osservice.New()
	if err != nil {
		return nil, err
	}
	services.Configure(layout.DataRoot)
	return &Manager{Layout: layout, Services: services, Prober: processProber{}, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Logger: observability.Discard(observability.ComponentInstaller)}, nil
}

func (manager *Manager) SetLogger(logger *observability.Logger) {
	manager.Logger = logger
}

func (manager *Manager) logger() *observability.Logger {
	return observability.CategoryLogger(manager.Logger, observability.ComponentInstaller, observability.CategoryInstall)
}

func (manager *Manager) Install(ctx context.Context, bundleRoot string) (returnErr error) {
	logger := manager.logger()
	started := time.Now()
	logger.Info("installation started", "event", "install.started")
	defer func() {
		if returnErr != nil {
			logger.Error("installation failed", "event", "install.failed", "duration", time.Since(started), "err", returnErr)
			return
		}
		logger.Info("installation completed", "event", "install.completed", "duration", time.Since(started))
	}()
	lock, err := acquireInstallLock(manager.Layout)
	if err != nil {
		return err
	}
	defer lock.Close()
	if info, err := os.Lstat(manager.Layout.ActiveManifest); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return errors.New("active install manifest is unsafe")
		}
		return errors.New("sumi is already installed; use upgrade")
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("inspect active install manifest")
	}
	bundle, err := releasebundle.Open(bundleRoot, manager.GOOS, manager.GOARCH)
	if err != nil {
		return err
	}
	logger.Info("release bundle verified", "event", "install.bundle.verified", "release_version", bundle.Manifest.ReleaseVersion, "operating_system", manager.GOOS, "architecture", manager.GOARCH)
	versionRoot := manager.Layout.VersionRoot(bundle.Manifest.ReleaseVersion)
	if err := bundle.CopyTo(versionRoot); err != nil {
		return err
	}
	logger.Info("release payload copied", "event", "install.release.copied", "release_version", bundle.Manifest.ReleaseVersion)
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(versionRoot)
		}
	}()
	if err := SaveActive(manager.Layout, bundle.Manifest); err != nil {
		return err
	}
	if err := manager.Services.Install(ctx, manager.serviceConfig(bundle.Manifest.ReleaseVersion)); err != nil {
		_ = manager.Services.Uninstall(ctx)
		_ = os.Remove(manager.Layout.ActiveManifest)
		return err
	}
	logger.Info("current-user services installed", "event", "install.services.installed", "release_version", bundle.Manifest.ReleaseVersion)
	cleanup = false
	return nil
}

func (manager *Manager) Active() (ActiveManifest, error) {
	return LoadActive(manager.Layout)
}

func (manager *Manager) serviceConfig(version string) osservice.InstallConfig {
	return osservice.InstallConfig{Binary: manager.Layout.Binary(version), WebRoot: manager.Layout.WebRoot(version), DataRoot: manager.Layout.DataRoot}
}

func (manager *Manager) hook(stage string) error {
	if manager.Before == nil {
		return nil
	}
	return manager.Before(stage)
}
