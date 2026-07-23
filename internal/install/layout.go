package install

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"

	"github.com/abcdlsj/sumi/internal/home"
	"github.com/abcdlsj/sumi/internal/userdirs"
)

type Layout struct {
	DataRoot       string
	StateRoot      string
	RuntimeRoot    string
	InstallRoot    string
	VersionsRoot   string
	ActiveManifest string
	RestoreRoot    string
	LockFile       string
}

func Resolve(dataRoot string) (Layout, error) {
	layout, err := Inspect(dataRoot)
	if err != nil {
		return Layout{}, err
	}
	homeLayout, err := home.Ensure(layout.DataRoot)
	if err != nil {
		return Layout{}, err
	}
	userLayout, err := userdirs.Ensure()
	if err != nil {
		return Layout{}, err
	}
	if homeLayout.Root != layout.DataRoot || userLayout.StateRoot != layout.StateRoot || userLayout.Runtime != layout.RuntimeRoot {
		return Layout{}, errors.New("install layout changed during initialization")
	}
	for _, path := range []string{layout.InstallRoot, layout.VersionsRoot} {
		if err := ensurePrivateDirectory(path); err != nil {
			return Layout{}, err
		}
	}
	return layout, nil
}

func Inspect(dataRoot string) (Layout, error) {
	homeDirectory, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(homeDirectory) {
		return Layout{}, errors.New("resolve user home")
	}
	if dataRoot == "" {
		dataRoot = filepath.Join(homeDirectory, ".sumi")
	}
	if !filepath.IsAbs(dataRoot) {
		return Layout{}, errors.New("data root must be absolute")
	}
	var stateRoot, runtimeRoot string
	switch runtime.GOOS {
	case "darwin":
		stateRoot = filepath.Join(homeDirectory, "Library", "Application Support", "Sumi")
		runtimeRoot = filepath.Join(homeDirectory, "Library", "Caches", "Sumi", "runtime")
	case "linux":
		stateHome := os.Getenv("XDG_STATE_HOME")
		if stateHome == "" {
			stateHome = filepath.Join(homeDirectory, ".local", "state")
		}
		if !filepath.IsAbs(stateHome) {
			return Layout{}, errors.New("xdg state home must be absolute")
		}
		stateRoot = filepath.Join(stateHome, "sumi")
		runtimeHome := os.Getenv("XDG_RUNTIME_DIR")
		if runtimeHome == "" {
			runtimeRoot = filepath.Join(stateRoot, "runtime")
		} else {
			if !filepath.IsAbs(runtimeHome) {
				return Layout{}, errors.New("xdg runtime directory must be absolute")
			}
			runtimeRoot = filepath.Join(runtimeHome, "sumi")
		}
	default:
		return Layout{}, errors.New("unsupported install operating system")
	}
	installRoot := filepath.Join(stateRoot, "install")
	layout := Layout{
		DataRoot: filepath.Clean(dataRoot), StateRoot: stateRoot, RuntimeRoot: runtimeRoot,
		InstallRoot: installRoot, VersionsRoot: filepath.Join(installRoot, "versions"),
		ActiveManifest: filepath.Join(installRoot, "active.json"), RestoreRoot: filepath.Join(installRoot, "restore"),
		LockFile: filepath.Join(runtimeRoot, "install.lock"),
	}
	return layout, nil
}

func (layout Layout) VersionRoot(version string) string {
	return filepath.Join(layout.VersionsRoot, version)
}

func (layout Layout) Binary(version string) string {
	return filepath.Join(layout.VersionRoot(version), "bin", "sumi")
}

func (layout Layout) WebRoot(version string) string {
	return filepath.Join(layout.VersionRoot(version), "web")
}

func ensurePrivateDirectory(path string) error {
	info, err := os.Lstat(path)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("install directory is unsafe")
		}
		if err := os.Chmod(path, 0o700); err != nil {
			return errors.New("secure install directory")
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return errors.New("inspect install directory")
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return errors.New("create install directory")
	}
	return nil
}
