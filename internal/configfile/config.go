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
	var config Config
	decoder := toml.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return Config{}, errors.New("sumi config is invalid")
	}
	if err := validate(config); err != nil {
		return Config{}, err
	}
	return config, nil
}

func Save(path string, config Config) error {
	if err := validate(config); err != nil {
		return err
	}
	payload, err := toml.Marshal(config)
	if err != nil {
		return errors.New("encode Sumi config")
	}
	if len(payload) > 64<<10 {
		return errors.New("sumi config is too large")
	}
	parent := filepath.Dir(path)
	if err := secureDirectory(parent); err != nil {
		return err
	}
	if err := validateDestination(path); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(parent, ".config-*.tmp")
	if err != nil {
		return errors.New("create temporary Sumi config")
	}
	temporaryPath := temporary.Name()
	removeTemporary := true
	defer func() {
		temporary.Close()
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return errors.New("secure temporary Sumi config")
	}
	if _, err := temporary.Write(payload); err != nil {
		return errors.New("write temporary Sumi config")
	}
	if err := temporary.Sync(); err != nil {
		return errors.New("sync temporary Sumi config")
	}
	if err := temporary.Close(); err != nil {
		return errors.New("close temporary Sumi config")
	}
	if err := validateDestination(path); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return errors.New("publish Sumi config")
	}
	removeTemporary = false
	if err := syncDirectory(parent); err != nil {
		return errors.New("sync Sumi config directory")
	}
	return nil
}

func validate(config Config) error {
	if config.Version != Version {
		return fmt.Errorf("unsupported sumi config version %d", config.Version)
	}
	for _, value := range []string{config.Server.Origin, config.Server.Identity, config.Server.SPKIPin} {
		if len(value) > 2048 || strings.ContainsRune(value, 0) {
			return errors.New("sumi config is invalid")
		}
	}
	return nil
}

func readSecure(path string) ([]byte, error) {
	descriptor, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		if errors.Is(err, unix.ENOENT) {
			return nil, os.ErrNotExist
		}
		return nil, errors.New("open Sumi config")
	}
	file := os.NewFile(uintptr(descriptor), path)
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return nil, errors.New("sumi config is unsafe")
	}
	payload, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	if err != nil {
		return nil, errors.New("read Sumi config")
	}
	if len(payload) > 64<<10 {
		return nil, errors.New("sumi config is too large")
	}
	return payload, nil
}

func secureDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm() != 0o700 {
		return errors.New("sumi config directory is unsafe")
	}
	return nil
}

func validateDestination(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return errors.New("sumi config is unsafe")
	}
	return nil
}

func syncDirectory(path string) error {
	descriptor, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	directory := os.NewFile(uintptr(descriptor), path)
	defer directory.Close()
	return directory.Sync()
}
