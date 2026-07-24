package credential

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

const keychainService = "co.sumi.credential"

type Facility interface {
	Kind() string
	Put(context.Context, string, []byte) error
	Get(context.Context, string) ([]byte, error)
	Delete(context.Context, string) error
}

func DiscoverFacility(goos string) (Facility, bool) {
	switch goos {
	case "darwin":
		if path, err := exec.LookPath("security"); err == nil {
			return commandFacility{kind: "macos_keychain", path: path}, true
		}
	case "linux":
		if path, err := exec.LookPath("secret-tool"); err == nil {
			return commandFacility{kind: "linux_secret_service", path: path}, true
		}
	}
	return nil, false
}

func CurrentFacility() (Facility, bool) {
	return DiscoverFacility(runtime.GOOS)
}

type commandFacility struct {
	kind string
	path string
}

func (facility commandFacility) Kind() string { return facility.kind }

func (facility commandFacility) Put(ctx context.Context, handle string, secret []byte) error {
	if err := validHandle(handle); err != nil || len(secret) == 0 || len(secret) > 64*1024 || bytes.IndexByte(secret, 0) >= 0 {
		return errors.New("credential binding material is invalid")
	}
	var command *exec.Cmd
	switch facility.kind {
	case "macos_keychain":
		command = exec.CommandContext(ctx, facility.path, "add-generic-password", "-U", "-a", handle, "-s", keychainService, "-w")
	case "linux_secret_service":
		command = exec.CommandContext(ctx, facility.path, "store", "--label=Sumi credential", "service", keychainService, "account", handle)
	default:
		return errors.New("credential facility is unavailable")
	}
	command.Stdin = bytes.NewReader(append(append([]byte(nil), secret...), '\n'))
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return errors.New("OS credential facility rejected the credential")
	}
	return nil
}

func (facility commandFacility) Get(ctx context.Context, handle string) ([]byte, error) {
	if err := validHandle(handle); err != nil {
		return nil, err
	}
	var command *exec.Cmd
	switch facility.kind {
	case "macos_keychain":
		command = exec.CommandContext(ctx, facility.path, "find-generic-password", "-a", handle, "-s", keychainService, "-w")
	case "linux_secret_service":
		command = exec.CommandContext(ctx, facility.path, "lookup", "service", keychainService, "account", handle)
	default:
		return nil, errors.New("credential facility is unavailable")
	}
	var stdout bytes.Buffer
	command.Stdout = &stdout
	if err := command.Run(); err != nil {
		return nil, errors.New("credential binding is unavailable")
	}
	secret := bytes.TrimSuffix(stdout.Bytes(), []byte{'\n'})
	if len(secret) == 0 || len(secret) > 64*1024 || bytes.IndexByte(secret, 0) >= 0 {
		return nil, errors.New("credential binding material is invalid")
	}
	return append([]byte(nil), secret...), nil
}

func (facility commandFacility) Delete(ctx context.Context, handle string) error {
	if err := validHandle(handle); err != nil {
		return err
	}
	var command *exec.Cmd
	switch facility.kind {
	case "macos_keychain":
		command = exec.CommandContext(ctx, facility.path, "delete-generic-password", "-a", handle, "-s", keychainService)
	case "linux_secret_service":
		command = exec.CommandContext(ctx, facility.path, "clear", "service", keychainService, "account", handle)
	default:
		return errors.New("credential facility is unavailable")
	}
	if err := command.Run(); err != nil {
		return errors.New("credential binding could not be deleted")
	}
	return nil
}

func validHandle(value string) error {
	if len(value) < 16 || len(value) > 255 || value != strings.TrimSpace(value) || strings.ContainsAny(value, "\x00\r\n") {
		return fmt.Errorf("credential binding handle is invalid")
	}
	return nil
}
