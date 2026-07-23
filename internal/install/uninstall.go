package install

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"

	"golang.org/x/sys/unix"
)

func PurgeCategories() []string {
	return []string{
		"config",
		"server facts and artifacts",
		"Computer identity and outbox",
		"Agent workspaces",
		"cache",
		"logs",
	}
}

func (manager *Manager) Uninstall(ctx context.Context, purge bool) error {
	lock, err := acquireInstallLock(manager.Layout)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := manager.stopServices(ctx); err != nil {
		return err
	}
	maintenance, err := manager.acquireMaintenanceAfterServiceStop(ctx)
	if err != nil {
		return err
	}
	defer maintenance.Close()
	if err := manager.Services.Uninstall(ctx); err != nil {
		return errors.New("remove current-user services")
	}
	if err := removeSafeTree(manager.Layout.InstallRoot); err != nil {
		return err
	}
	if purge {
		credential, err := prepareCredentialPurge(manager.Layout.StateRoot)
		if err != nil {
			return err
		}
		defer credential.close()
		if err := purgeDataRoot(manager.Layout.DataRoot); err != nil {
			return err
		}
		return credential.remove()
	}
	return nil
}

type credentialPurge struct {
	state       *os.File
	credentials *os.File
	human       *unix.Stat_t
}

func prepareCredentialPurge(stateRoot string) (*credentialPurge, error) {
	stateDescriptor, err := unix.Open(stateRoot, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, errors.New("purge credential state is unsafe")
	}
	state := os.NewFile(uintptr(stateDescriptor), stateRoot)
	credentialDescriptor, err := unix.Openat(stateDescriptor, "credentials", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		state.Close()
		return nil, errors.New("purge credential directory is unsafe")
	}
	credentials := os.NewFile(uintptr(credentialDescriptor), filepath.Join(stateRoot, "credentials"))
	purge := &credentialPurge{state: state, credentials: credentials}
	if err := purge.inspect(); err != nil {
		purge.close()
		return nil, err
	}
	return purge, nil
}

func (purge *credentialPurge) inspect() error {
	if _, err := purge.credentials.Seek(0, 0); err != nil {
		return errors.New("inspect purge credential directory")
	}
	entries, err := purge.credentials.ReadDir(-1)
	if err != nil {
		return errors.New("inspect purge credential directory")
	}
	if len(entries) == 0 {
		purge.human = nil
		return nil
	}
	if len(entries) != 1 || entries[0].Name() != "human.key" {
		return errors.New("purge credential directory contains an unexpected entry")
	}
	var info unix.Stat_t
	if err := unix.Fstatat(int(purge.credentials.Fd()), "human.key", &info, unix.AT_SYMLINK_NOFOLLOW); err != nil || info.Mode&unix.S_IFMT != unix.S_IFREG || info.Mode&0o777 != 0o600 {
		return errors.New("purge human credential is unsafe")
	}
	purge.human = &info
	return nil
}

func (purge *credentialPurge) remove() error {
	expected := purge.human
	if err := purge.inspect(); err != nil {
		return err
	}
	if expected == nil != (purge.human == nil) || expected != nil && (expected.Dev != purge.human.Dev || expected.Ino != purge.human.Ino) {
		return errors.New("purge human credential changed during uninstall")
	}
	if purge.human != nil {
		if err := unix.Unlinkat(int(purge.credentials.Fd()), "human.key", 0); err != nil {
			return errors.New("remove human credential")
		}
		if err := purge.credentials.Sync(); err != nil {
			return errors.New("sync credential directory")
		}
	}
	if err := unix.Unlinkat(int(purge.state.Fd()), "credentials", unix.AT_REMOVEDIR); err != nil {
		return errors.New("remove credential directory")
	}
	if err := purge.state.Sync(); err != nil {
		return errors.New("sync credential state")
	}
	return nil
}

func (purge *credentialPurge) close() {
	_ = purge.credentials.Close()
	_ = purge.state.Close()
}

func purgeDataRoot(root string) error {
	homeDirectory, err := os.UserHomeDir()
	if err != nil {
		return errors.New("resolve user home for purge")
	}
	root = filepath.Clean(root)
	homeDirectory = filepath.Clean(homeDirectory)
	if !filepath.IsAbs(root) || root == string(filepath.Separator) || root == homeDirectory {
		return errors.New("purge data root is unsafe")
	}
	relative, err := filepath.Rel(homeDirectory, root)
	if err != nil || relative == "." || relative == ".." || len(relative) >= 3 && relative[:3] == ".."+string(filepath.Separator) {
		return errors.New("purge data root is outside user home")
	}
	info, err := os.Lstat(root)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("purge data root is unsafe")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return errors.New("inspect purge data root")
	}
	want := []string{"agents", "cache", "config.toml", "data", "logs"}
	actual := make([]string, 0, len(entries))
	for _, entry := range entries {
		actual = append(actual, entry.Name())
		entryInfo, err := os.Lstat(filepath.Join(root, entry.Name()))
		if err != nil || entryInfo.Mode()&os.ModeSymlink != 0 {
			return errors.New("purge entry is unsafe")
		}
	}
	sort.Strings(actual)
	for index := range want {
		if len(actual) != len(want) || actual[index] != want[index] {
			return errors.New("purge data root contains an unexpected entry")
		}
	}
	for _, name := range []string{"data", "agents", "cache", "logs"} {
		if err := removeSafeTree(filepath.Join(root, name)); err != nil {
			return err
		}
	}
	config := filepath.Join(root, "config.toml")
	configInfo, err := os.Lstat(config)
	if err != nil || configInfo.Mode()&os.ModeSymlink != 0 || !configInfo.Mode().IsRegular() {
		return errors.New("purge config is unsafe")
	}
	if err := os.Remove(config); err != nil {
		return err
	}
	return os.Remove(root)
}
