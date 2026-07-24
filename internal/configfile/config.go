package configfile

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"golang.org/x/sys/unix"
)

const Version = 1

type Config struct {
	Version int          `toml:"version"`
	Server  ServerConfig `toml:"server,omitempty"`
}

type ServerConfig struct {
	Origin   string `toml:"origin,omitempty"`
	Identity string `toml:"identity,omitempty"`
	SPKIPin  string `toml:"spki_pin,omitempty"`
}

func Default() Config {
	return Config{Version: Version}
}

func Ensure(path string) error {
	_, err := Load(path)
	if err == nil {
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return Save(path, Default())
}

func Load(path string) (Config, error) {
	payload, err := readSecure(path)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	dec := toml.NewDecoder(bytes.NewReader(payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, errors.New("sumi config is invalid")
	}
	if err := validate(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func Save(path string, cfg Config) error {
	if err := validate(cfg); err != nil {
		return err
	}
	payload, err := toml.Marshal(cfg)
	if err != nil {
		return errors.New("encode Sumi config")
	}
	if len(payload) > 64<<10 {
		return errors.New("sumi config is too large")
	}
	parent := filepath.Dir(path)
	if err := secureDir(parent); err != nil {
		return err
	}
	if err := checkDest(path); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(parent, ".config-*.tmp")
	if err != nil {
		return errors.New("create temporary Sumi config")
	}
	tmpPath := tmp.Name()
	remove := true
	defer func() {
		tmp.Close()
		if remove {
			os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return errors.New("secure temporary Sumi config")
	}
	if _, err := tmp.Write(payload); err != nil {
		return errors.New("write temporary Sumi config")
	}
	if err := tmp.Sync(); err != nil {
		return errors.New("sync temporary Sumi config")
	}
	if err := tmp.Close(); err != nil {
		return errors.New("close temporary Sumi config")
	}
	if err := checkDest(path); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return errors.New("publish Sumi config")
	}
	remove = false
	if err := syncDir(parent); err != nil {
		return errors.New("sync Sumi config directory")
	}
	return nil
}

func validate(cfg Config) error {
	if cfg.Version != Version {
		return fmt.Errorf("unsupported sumi config version %d", cfg.Version)
	}
	for _, v := range []string{cfg.Server.Origin, cfg.Server.Identity, cfg.Server.SPKIPin} {
		if len(v) > 2048 || strings.ContainsRune(v, 0) {
			return errors.New("sumi config is invalid")
		}
	}
	return nil
}

func readSecure(path string) ([]byte, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		if errors.Is(err, unix.ENOENT) {
			return nil, os.ErrNotExist
		}
		return nil, errors.New("open Sumi config")
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return nil, errors.New("sumi config is unsafe")
	}
	payload, err := io.ReadAll(io.LimitReader(f, (64<<10)+1))
	if err != nil {
		return nil, errors.New("read Sumi config")
	}
	if len(payload) > 64<<10 {
		return nil, errors.New("sumi config is too large")
	}
	return payload, nil
}

func secureDir(path string) error {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm() != 0o700 {
		return errors.New("sumi config directory is unsafe")
	}
	return nil
}

func checkDest(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return errors.New("sumi config is unsafe")
	}
	return nil
}

func syncDir(path string) error {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	dir := os.NewFile(uintptr(fd), path)
	defer dir.Close()
	return dir.Sync()
}
