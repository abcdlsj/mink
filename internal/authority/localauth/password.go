package localauth

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

var usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{2,31}$`)

type PasswordParameters struct {
	Memory      uint32
	Iterations  uint32
	Parallelism uint8
}

func DefaultPasswordParameters() PasswordParameters {
	return PasswordParameters{Memory: 64 * 1024, Iterations: 3, Parallelism: 2}
}

func (p PasswordParameters) Valid() bool {
	return p.Memory >= 8192 && p.Memory <= 262144 &&
		p.Iterations >= 1 && p.Iterations <= 10 &&
		p.Parallelism >= 1 && p.Parallelism <= 8
}

func NormalizeUsername(s string) (string, bool) {
	if s != strings.TrimSpace(s) || !utf8.ValidString(s) {
		return "", false
	}
	s = strings.ToLower(s)
	return s, usernamePattern.MatchString(s)
}

func ValidNewPassword(password string) bool {
	if !utf8.ValidString(password) || len(password) > 1024 || strings.TrimSpace(password) == "" {
		return false
	}
	n := utf8.RuneCountInString(password)
	return n >= 12 && n <= 256
}

func HashPassword(r io.Reader, password string, p PasswordParameters) (authorityapp.PasswordDigest, error) {
	if !ValidNewPassword(password) || !p.Valid() {
		return authorityapp.PasswordDigest{}, authorityapp.ErrLocalAccountInvalid
	}
	salt := make([]byte, passwordSaltSize)
	if _, err := io.ReadFull(r, salt); err != nil {
		return authorityapp.PasswordDigest{}, fmt.Errorf("generate password salt: %w", err)
	}
	return authorityapp.PasswordDigest{
		Algorithm: passwordAlgorithm, Salt: salt,
		Digest: derivePassword(password, salt, p),
		Memory: p.Memory, Iterations: p.Iterations, Parallelism: p.Parallelism,
	}, nil
}

func VerifyPassword(password string, d authorityapp.PasswordDigest) bool {
	p := PasswordParameters{
		Memory: d.Memory, Iterations: d.Iterations, Parallelism: d.Parallelism,
	}
	if d.Algorithm != passwordAlgorithm || len(d.Salt) != passwordSaltSize || len(d.Digest) != passwordKeySize || !p.Valid() || len(password) > 1024 {
		return false
	}
	got := derivePassword(password, d.Salt, p)
	return subtle.ConstantTimeCompare(got, d.Digest) == 1
}

func VerifyDummyPassword(password string, p PasswordParameters) {
	if len(password) > 1024 {
		password = password[:1024]
	}
	_ = derivePassword(password, []byte("sumi-login-dummy"), p)
}

func derivePassword(password string, salt []byte, p PasswordParameters) []byte {
	return argon2.IDKey([]byte(password), salt, p.Iterations, p.Memory, p.Parallelism, passwordKeySize)
}
