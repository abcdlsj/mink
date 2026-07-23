package websession

import (
	"crypto/subtle"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"

	authorityapp "github.com/abcdlsj/sumi/internal/authority/application"
	"golang.org/x/crypto/argon2"
)

const (
	passwordAlgorithm = "argon2id"
	passwordSaltSize  = 16
	passwordKeySize   = 32
)

var localUsernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{2,31}$`)

type passwordParameters struct {
	memory      uint32
	iterations  uint32
	parallelism uint8
}

func defaultPasswordParameters() passwordParameters {
	return passwordParameters{memory: 64 * 1024, iterations: 3, parallelism: 2}
}

func (parameters passwordParameters) valid() bool {
	return parameters.memory >= 8192 && parameters.memory <= 262144 &&
		parameters.iterations >= 1 && parameters.iterations <= 10 &&
		parameters.parallelism >= 1 && parameters.parallelism <= 8
}

func normalizeLocalUsername(username string) (string, bool) {
	if username != strings.TrimSpace(username) || !utf8.ValidString(username) {
		return "", false
	}
	canonical := strings.ToLower(username)
	return canonical, localUsernamePattern.MatchString(canonical)
}

func validNewPassword(password string) bool {
	if !utf8.ValidString(password) || len(password) > 1024 || strings.TrimSpace(password) == "" {
		return false
	}
	characters := utf8.RuneCountInString(password)
	return characters >= 12 && characters <= 256
}

func hashLocalPassword(random io.Reader, password string, parameters passwordParameters) (authorityapp.PasswordDigest, error) {
	if !validNewPassword(password) || !parameters.valid() {
		return authorityapp.PasswordDigest{}, authorityapp.ErrLocalAccountInvalid
	}
	salt := make([]byte, passwordSaltSize)
	if _, err := io.ReadFull(random, salt); err != nil {
		return authorityapp.PasswordDigest{}, fmt.Errorf("generate password salt: %w", err)
	}
	return authorityapp.PasswordDigest{
		Algorithm: passwordAlgorithm, Salt: salt,
		Digest: deriveLocalPassword(password, salt, parameters),
		Memory: parameters.memory, Iterations: parameters.iterations, Parallelism: parameters.parallelism,
	}, nil
}

func verifyLocalPassword(password string, digest authorityapp.PasswordDigest) bool {
	parameters := passwordParameters{
		memory: digest.Memory, iterations: digest.Iterations, parallelism: digest.Parallelism,
	}
	if digest.Algorithm != passwordAlgorithm || len(digest.Salt) != passwordSaltSize || len(digest.Digest) != passwordKeySize || !parameters.valid() || len(password) > 1024 {
		return false
	}
	candidate := deriveLocalPassword(password, digest.Salt, parameters)
	return subtle.ConstantTimeCompare(candidate, digest.Digest) == 1
}

func verifyDummyPassword(password string, parameters passwordParameters) {
	if len(password) > 1024 {
		password = password[:1024]
	}
	_ = deriveLocalPassword(password, []byte("sumi-login-dummy"), parameters)
}

func deriveLocalPassword(password string, salt []byte, parameters passwordParameters) []byte {
	return argon2.IDKey([]byte(password), salt, parameters.iterations, parameters.memory, parameters.parallelism, passwordKeySize)
}
