package install

import (
	"context"
	"errors"
	"os"
	"runtime"

	"github.com/abcdlsj/sumi/internal/lifecycle"
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
	return &Manager{Layout: layout, Services: services, Prober: processProber{}, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}, nil
}

func (manager *Manager) Install(ctx context.Context, bundleRoot string) error {
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
	versionRoot := manager.Layout.VersionRoot(bundle.Manifest.ReleaseVersion)
	if err := bundle.CopyTo(versionRoot); err != nil {
		return err
	}
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
