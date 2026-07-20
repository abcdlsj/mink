package computerhost

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

func ReadRegistrationKey(path string) (string, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("inspect registration key file: %w", err)
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return "", errors.New("registration key file is not a regular file")
	}
	if before.Mode().Perm() != 0o600 {
		return "", fmt.Errorf("registration key file mode is %o, want 600", before.Mode().Perm())
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read registration key file: %w", err)
	}
	after, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("reinspect registration key file: %w", err)
	}
	if !os.SameFile(before, after) || after.Mode()&os.ModeSymlink != 0 || !after.Mode().IsRegular() || after.Mode().Perm() != 0o600 {
		return "", errors.New("registration key file changed while reading")
	}
	key := strings.TrimSpace(string(content))
	if key == "" {
		return "", errors.New("registration key file is empty")
	}
	if len(key) > 256 {
		return "", errors.New("registration key is too long")
	}
	return key, nil
}
