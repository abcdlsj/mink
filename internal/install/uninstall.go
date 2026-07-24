package install

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"
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

func (manager *Manager) Uninstall(ctx context.Context, purge bool) (returnErr error) {
	logger := manager.logger()
	started := time.Now()
	logger.Info("uninstall started", "event", "install.uninstall.started", "purge_data", purge)
	defer func() {
		if returnErr != nil {
			logger.Error("uninstall failed", "event", "install.uninstall.failed", "purge_data", purge, "duration", time.Since(started), "err", returnErr)
			return
		}
		logger.Info("uninstall completed", "event", "install.uninstall.completed", "purge_data", purge, "duration", time.Since(started))
	}()
	lock, err := acquireInstallLock(manager.Layout)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := manager.stopServices(ctx); err != nil {
		return err
	}
	logger.Info("services stopped for uninstall", "event", "install.uninstall.services_stopped")
	maintenance, err := manager.acquireMaintenanceAfterServiceStop(ctx)
	if err != nil {
		return err
	}
	defer maintenance.Close()
	if err := manager.Services.Uninstall(ctx); err != nil {
		return errors.New("remove current-user services")
	}
	logger.Info("current-user services removed", "event", "install.uninstall.services_removed")
	if err := removeSafeTree(manager.Layout.InstallRoot); err != nil {
		return err
	}
	logger.Info("installation payload removed", "event", "install.uninstall.payload_removed")
	if purge {
		if err := purgeDataRoot(manager.Layout.DataRoot); err != nil {
			return err
		}
		logger.Warn("persistent data purged", "event", "install.uninstall.data_purged")
		return nil
	}
	return nil
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
