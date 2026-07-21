package computerhost

import (
	"errors"
	"strings"
)

func ReadRegistrationKey(path string) (string, error) {
	content, err := readSecureFile(path, "registration key")
	if err != nil {
		return "", err
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
