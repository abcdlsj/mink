package install

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/abcdlsj/sumi/internal/releasebundle"
	"golang.org/x/sys/unix"
)

const activeManifestVersion = 1

type ActiveManifest struct {
	Version  int                    `json:"version"`
	DataRoot string                 `json:"data_root"`
	Release  releasebundle.Manifest `json:"release"`
}

func LoadActive(layout Layout) (ActiveManifest, error) {
	payload, err := readSecureFile(layout.ActiveManifest, 1<<20)
	if err != nil {
		return ActiveManifest{}, err
	}
	return decodeActive(payload)
}

func SaveActive(layout Layout, release releasebundle.Manifest) error {
	manifest := ActiveManifest{Version: activeManifestVersion, DataRoot: layout.DataRoot, Release: release}
	if _, err := decodeActive(mustJSON(manifest)); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	return writeAtomic(layout.ActiveManifest, payload, 0o600)
}

func decodeActive(payload []byte) (ActiveManifest, error) {
	var manifest ActiveManifest
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return ActiveManifest{}, errors.New("active install manifest is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ActiveManifest{}, errors.New("active install manifest is invalid")
	}
	if manifest.Version != activeManifestVersion || releasebundle.ValidateManifest(manifest.Release) != nil {
		return ActiveManifest{}, errors.New("active install manifest is invalid")
	}
	if !filepath.IsAbs(manifest.DataRoot) || filepath.Clean(manifest.DataRoot) != manifest.DataRoot || strings.ContainsRune(manifest.DataRoot, 0) {
		return ActiveManifest{}, errors.New("active install manifest is invalid")
	}
	return manifest, nil
}

func readSecureFile(path string, limit int64) ([]byte, error) {
	descriptor, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		if errors.Is(err, unix.ENOENT) {
			return nil, os.ErrNotExist
		}
		return nil, err
	}
	file := os.NewFile(uintptr(descriptor), path)
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return nil, errors.New("install file is unsafe")
	}
	payload, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(payload)) > limit {
		return nil, errors.New("install file is invalid")
	}
	return payload, nil
}

func writeAtomic(path string, payload []byte, mode os.FileMode) error {
	parent := filepath.Dir(path)
	if err := ensurePrivateDirectory(parent); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil && (info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular()) {
		return errors.New("install file is unsafe")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.New("inspect install file")
	}
	temporary, err := os.CreateTemp(parent, ".sumi-install-*.tmp")
	if err != nil {
		return errors.New("create temporary install file")
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode); err != nil {
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
		return errors.New("publish install file")
	}
	directory, err := os.Open(parent)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func mustJSON(value any) []byte {
	payload, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return payload
}
