package authority

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
)

const minCredLen = 43
const maxCredLen = 128

func validCred(s string) bool {
	if n := len(s); n < minCredLen || n > maxCredLen {
		return false
	}
	for _, r := range s {
		if !((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

func EnsureBootstrapCredential(path string, authorityExists bool) (string, error) {
	cred, err := ReadCredentialFile(path)
	if err == nil {
		return cred, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if authorityExists {
		return "", fmt.Errorf("bootstrap credential missing: %w", os.ErrNotExist)
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate bootstrap credential: %w", err)
	}
	cred = base64.RawURLEncoding.EncodeToString(buf)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return ReadCredentialFile(path)
	}
	if err != nil {
		return "", fmt.Errorf("create bootstrap credential: %w", err)
	}
	if _, err := f.WriteString(cred); err != nil {
		f.Close()
		return "", fmt.Errorf("write bootstrap credential: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return "", fmt.Errorf("sync bootstrap credential: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("close bootstrap credential: %w", err)
	}
	return ReadCredentialFile(path)
}

func ReadCredentialFile(path string) (string, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return "", err
	}
	f := os.NewFile(uintptr(fd), path)
	if f == nil {
		syscall.Close(fd)
		return "", fmt.Errorf("open credential file")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("inspect credential file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("credential file is not a regular file")
	}
	if info.Mode().Perm() != 0o600 {
		return "", fmt.Errorf("credential file mode is %o, want 600", info.Mode().Perm())
	}
	payload, err := io.ReadAll(io.LimitReader(f, maxCredLen+1))
	if err != nil {
		return "", fmt.Errorf("read credential file: %w", err)
	}
	cred := string(payload)
	if !validCred(cred) {
		return "", fmt.Errorf("credential file does not contain a high-entropy credential")
	}
	return cred, nil
}

func ValidCredential(cred string) bool {
	return validCred(cred)
}
