package install

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"

	"golang.org/x/sys/unix"
)

const restoreReceiptVersion = 1

type restoreEntry struct {
	Name    string `json:"name"`
	Present bool   `json:"present"`
	SHA256  string `json:"sha256,omitempty"`
	Size    int64  `json:"size,omitempty"`
}

type restoreReceipt struct {
	Version        int            `json:"version"`
	ReleaseVersion string         `json:"release_version"`
	Entries        []restoreEntry `json:"entries"`
}

type restorePoint struct {
	layout  Layout
	receipt restoreReceipt
}

func createRestorePoint(layout Layout, active ActiveManifest) (*restorePoint, error) {
	if info, err := os.Lstat(layout.RestoreRoot); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return nil, errors.New("restore residue is unsafe")
		}
		return nil, errors.New("an unfinished restore point already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("inspect restore residue")
	}
	if err := os.Mkdir(layout.RestoreRoot, 0o700); err != nil {
		return nil, errors.New("create restore point")
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(layout.RestoreRoot)
		}
	}()
	receipt := restoreReceipt{Version: restoreReceiptVersion, ReleaseVersion: active.Release.ReleaseVersion}
	for _, name := range restoreNames() {
		source := restoreTarget(layout, name)
		entry := restoreEntry{Name: name}
		info, err := os.Lstat(source)
		if errors.Is(err, os.ErrNotExist) {
			if name == "server.db" || name == "config.toml" || name == "active.json" {
				return nil, errors.New("required restore source is missing")
			}
			receipt.Entries = append(receipt.Entries, entry)
			continue
		}
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, errors.New("restore source is unsafe")
		}
		backup := filepath.Join(layout.RestoreRoot, name)
		digest, size, err := copyNewWithHash(source, backup)
		if err != nil {
			return nil, err
		}
		entry.Present = true
		entry.SHA256 = digest
		entry.Size = size
		receipt.Entries = append(receipt.Entries, entry)
	}
	payload, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return nil, err
	}
	payload = append(payload, '\n')
	if err := writeAtomic(filepath.Join(layout.RestoreRoot, "receipt.json"), payload, 0o600); err != nil {
		return nil, err
	}
	point := &restorePoint{layout: layout, receipt: receipt}
	if err := point.Validate(); err != nil {
		return nil, err
	}
	cleanup = false
	return point, nil
}

func loadRestorePoint(layout Layout) (*restorePoint, error) {
	payload, err := readSecureFile(filepath.Join(layout.RestoreRoot, "receipt.json"), 64<<10)
	if err != nil {
		return nil, err
	}
	var receipt restoreReceipt
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&receipt); err != nil {
		return nil, errors.New("restore receipt is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("restore receipt is invalid")
	}
	point := &restorePoint{layout: layout, receipt: receipt}
	if err := point.Validate(); err != nil {
		return nil, err
	}
	return point, nil
}

func (point *restorePoint) Validate() error {
	if point == nil || point.receipt.Version != restoreReceiptVersion || point.receipt.ReleaseVersion == "" || len(point.receipt.Entries) != len(restoreNames()) {
		return ErrRestoreUnproven
	}
	want := restoreNames()
	actual := make([]string, 0, len(point.receipt.Entries))
	for _, entry := range point.receipt.Entries {
		actual = append(actual, entry.Name)
		if !entry.Present {
			if entry.SHA256 != "" || entry.Size != 0 {
				return ErrRestoreUnproven
			}
			continue
		}
		backup := filepath.Join(point.layout.RestoreRoot, entry.Name)
		digest, size, err := hashSecureFile(backup)
		if err != nil || size != entry.Size || digest != entry.SHA256 {
			return ErrRestoreUnproven
		}
	}
	sort.Strings(actual)
	sort.Strings(want)
	for index := range want {
		if want[index] != actual[index] {
			return ErrRestoreUnproven
		}
	}
	return nil
}

func (point *restorePoint) Restore() error {
	if err := point.Validate(); err != nil {
		return ErrRestoreUnproven
	}
	for _, entry := range point.receipt.Entries {
		target := restoreTarget(point.layout, entry.Name)
		if !entry.Present {
			if err := removeRegularIfPresent(target); err != nil {
				return ErrRestoreUnproven
			}
			continue
		}
		if err := copyAtomic(filepath.Join(point.layout.RestoreRoot, entry.Name), target); err != nil {
			return ErrRestoreUnproven
		}
	}
	active, err := LoadActive(point.layout)
	if err != nil || active.Release.ReleaseVersion != point.receipt.ReleaseVersion {
		return ErrRestoreUnproven
	}
	return nil
}

func (point *restorePoint) Cleanup() error {
	if point == nil {
		return nil
	}
	return removeSafeTree(point.layout.RestoreRoot)
}

func restoreNames() []string {
	return []string{"active.json", "config.toml", "server.db", "server.db-shm", "server.db-wal"}
}

func restoreTarget(layout Layout, name string) string {
	switch name {
	case "active.json":
		return layout.ActiveManifest
	case "config.toml":
		return filepath.Join(layout.DataRoot, "config.toml")
	case "server.db", "server.db-shm", "server.db-wal":
		return filepath.Join(layout.DataRoot, "data", name)
	default:
		panic("unknown restore target")
	}
}

func copyNewWithHash(source, target string) (string, int64, error) {
	sourceFile, info, err := openRegularNoFollow(source)
	if err != nil {
		return "", 0, err
	}
	defer sourceFile.Close()
	targetFile, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", 0, err
	}
	failed := true
	defer func() {
		targetFile.Close()
		if failed {
			_ = os.Remove(target)
		}
	}()
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(targetFile, hash), sourceFile)
	if err != nil || written != info.Size() {
		return "", 0, errors.New("copy restore source")
	}
	if err := targetFile.Sync(); err != nil {
		return "", 0, err
	}
	if err := targetFile.Close(); err != nil {
		return "", 0, err
	}
	failed = false
	return hex.EncodeToString(hash.Sum(nil)), written, nil
}

func copyAtomic(source, target string) error {
	sourceFile, info, err := openRegularNoFollow(source)
	if err != nil {
		return err
	}
	defer sourceFile.Close()
	if err := ensurePrivateDirectory(filepath.Dir(target)); err != nil {
		return err
	}
	if targetInfo, err := os.Lstat(target); err == nil && (targetInfo.Mode()&os.ModeSymlink != 0 || !targetInfo.Mode().IsRegular()) {
		return errors.New("restore target is unsafe")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(target), ".sumi-restore-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	written, err := io.Copy(temporary, sourceFile)
	if err != nil || written != info.Size() {
		temporary.Close()
		return errors.New("restore copy is incomplete")
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, target); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(target))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func hashSecureFile(path string) (string, int64, error) {
	file, info, err := openRegularNoFollow(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	written, err := io.Copy(hash, file)
	if err != nil || written != info.Size() {
		return "", 0, errors.New("hash restore file")
	}
	return hex.EncodeToString(hash.Sum(nil)), written, nil
}

func openRegularNoFollow(path string) (*os.File, os.FileInfo, error) {
	descriptor, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, nil, err
	}
	file := os.NewFile(uintptr(descriptor), path)
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		file.Close()
		return nil, nil, errors.New("file is not regular")
	}
	return file, info, nil
}

func removeRegularIfPresent(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("restore target is unsafe")
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func removeSafeTree(root string) error {
	info, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("remove target is unsafe")
	}
	if err := filepath.WalkDir(root, func(_ string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("remove target contains a symlink")
		}
		return nil
	}); err != nil {
		return err
	}
	return os.RemoveAll(root)
}
