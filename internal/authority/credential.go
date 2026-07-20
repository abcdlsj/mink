package authority

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"syscall"
)

var credentialPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43,128}$`)

func EnsureBootstrapCredential(path string, authorityExists bool) (string, error) {
	credential, err := ReadCredentialFile(path)
	if err == nil {
		return credential, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if authorityExists {
		return "", fmt.Errorf("bootstrap credential missing: %w", os.ErrNotExist)
	}

	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate bootstrap credential: %w", err)
	}
	credential = base64.RawURLEncoding.EncodeToString(random)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return ReadCredentialFile(path)
	}
	if err != nil {
		return "", fmt.Errorf("create bootstrap credential: %w", err)
	}
	if _, err := file.WriteString(credential); err != nil {
		file.Close()
		return "", fmt.Errorf("write bootstrap credential: %w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return "", fmt.Errorf("sync bootstrap credential: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("close bootstrap credential: %w", err)
	}
	return ReadCredentialFile(path)
}

func ReadCredentialFile(path string) (string, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return "", err
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		syscall.Close(fd)
		return "", fmt.Errorf("open credential file")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("inspect credential file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("credential file is not a regular file")
	}
	if info.Mode().Perm() != 0o600 {
		return "", fmt.Errorf("credential file mode is %o, want 600", info.Mode().Perm())
	}
	payload, err := io.ReadAll(io.LimitReader(file, 129))
	if err != nil {
		return "", fmt.Errorf("read credential file: %w", err)
	}
	credential := string(payload)
	if !credentialPattern.MatchString(credential) {
		return "", fmt.Errorf("credential file does not contain a high-entropy credential")
	}
	return credential, nil
}

func ValidCredential(credential string) bool {
	return credentialPattern.MatchString(credential)
}
