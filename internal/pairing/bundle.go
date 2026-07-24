package pairing

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/abcdlsj/sumi/internal/endpoint"
	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

const (
	Version           = 1
	CodePrefix        = "sumi-pair-v1."
	maxEncodedPayload = 16 << 10
)

var ErrStillValid = errors.New("pairing bundle is still valid")

type ServerIdentity struct {
	Kind    endpoint.IdentityKind `json:"kind"`
	SPKIPin string                `json:"spki_pin,omitempty"`
}

type Bundle struct {
	Version        int            `json:"version"`
	RequestID      string         `json:"request_id"`
	ServerOrigin   string         `json:"server_origin"`
	ServerIdentity ServerIdentity `json:"server_identity"`
	PairingToken   string         `json:"pairing_token"`
	ExpiresAt      time.Time      `json:"expires_at"`
}

type Opened struct {
	Bundle Bundle
	path   string
	file   *os.File
}

func New(server endpoint.Endpoint, expiresAt time.Time) (Bundle, error) {
	payload := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, payload); err != nil {
		return Bundle{}, errors.New("generate pairing token")
	}
	bundle := Bundle{
		Version: Version, RequestID: uuid.NewString(), ServerOrigin: server.Origin,
		ServerIdentity: ServerIdentity{Kind: server.Identity.Kind, SPKIPin: server.Identity.SPKIPin},
		PairingToken:   base64.RawURLEncoding.EncodeToString(payload), ExpiresAt: expiresAt.UTC(),
	}
	if _, err := bundle.Endpoint(); err != nil {
		return Bundle{}, err
	}
	return bundle, nil
}

func (b Bundle) Endpoint() (endpoint.Endpoint, error) {
	if err := validateBundle(b); err != nil {
		return endpoint.Endpoint{}, err
	}
	return endpoint.FromIdentity(b.ServerOrigin, endpoint.Identity{Kind: b.ServerIdentity.Kind, SPKIPin: b.ServerIdentity.SPKIPin})
}

func (b Bundle) ValidateAt(now time.Time) error {
	if _, err := b.Endpoint(); err != nil {
		return err
	}
	if !now.Before(b.ExpiresAt) {
		return errors.New("pairing bundle is expired")
	}
	return nil
}

// EncodeCode produces the copyable pairing representation used by the Web
// onboarding flow. The code is an encoding, not encryption; the pairing token
// remains the secret and is protected by its short expiry and one-time use.
func EncodeCode(bundle Bundle) (string, error) {
	if _, err := bundle.Endpoint(); err != nil {
		return "", err
	}
	payload, err := json.Marshal(bundle)
	if err != nil || len(payload) > maxEncodedPayload {
		return "", errors.New("encode pairing code")
	}
	return CodePrefix + base64.RawURLEncoding.EncodeToString(payload), nil
}

func DecodeCode(code string) (Bundle, error) {
	if !strings.HasPrefix(code, CodePrefix) {
		return Bundle{}, errors.New("pairing code is invalid")
	}
	encoded := strings.TrimPrefix(code, CodePrefix)
	if encoded == "" || len(encoded) > base64.RawURLEncoding.EncodedLen(maxEncodedPayload) {
		return Bundle{}, errors.New("pairing code is invalid")
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(payload) > maxEncodedPayload || base64.RawURLEncoding.EncodeToString(payload) != encoded {
		return Bundle{}, errors.New("pairing code is invalid")
	}
	var bundle Bundle
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&bundle); err != nil {
		return Bundle{}, errors.New("pairing code is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Bundle{}, errors.New("pairing code is invalid")
	}
	if _, err := bundle.Endpoint(); err != nil {
		return Bundle{}, errors.New("pairing code is invalid")
	}
	return bundle, nil
}

func WriteNew(path string, bundle Bundle) error {
	if _, err := bundle.Endpoint(); err != nil {
		return err
	}
	payload, err := json.Marshal(bundle)
	if err != nil || len(payload) > maxEncodedPayload {
		return errors.New("encode pairing bundle")
	}
	payload = append(payload, '\n')
	parent := filepath.Dir(path)
	info, err := os.Lstat(parent)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("pairing bundle directory is unsafe")
	}
	temporary, err := os.CreateTemp(parent, ".pairing-*.tmp")
	if err != nil {
		return errors.New("create temporary pairing bundle")
	}
	temporaryPath := temporary.Name()
	defer func() {
		temporary.Close()
		_ = os.Remove(temporaryPath)
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return errors.New("secure temporary pairing bundle")
	}
	if _, err := temporary.Write(payload); err != nil {
		return errors.New("write temporary pairing bundle")
	}
	if err := temporary.Sync(); err != nil {
		return errors.New("sync temporary pairing bundle")
	}
	if err := temporary.Close(); err != nil {
		return errors.New("close temporary pairing bundle")
	}
	if err := unix.Linkat(unix.AT_FDCWD, temporaryPath, unix.AT_FDCWD, path, 0); err != nil {
		if errors.Is(err, unix.EEXIST) {
			return errors.New("pairing bundle already exists")
		}
		return errors.New("publish pairing bundle")
	}
	if err := syncDirectory(parent); err != nil {
		return errors.New("sync pairing bundle directory")
	}
	if err := os.Remove(temporaryPath); err != nil {
		return errors.New("remove temporary pairing bundle")
	}
	if err := syncDirectory(parent); err != nil {
		return errors.New("sync pairing bundle directory")
	}
	return nil
}

func Open(path string) (*Opened, error) {
	descriptor, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, errors.New("open pairing bundle")
	}
	file := os.NewFile(uintptr(descriptor), path)
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		file.Close()
		return nil, errors.New("pairing bundle is unsafe")
	}
	payload, err := io.ReadAll(io.LimitReader(file, maxEncodedPayload+1))
	if err != nil || len(payload) > maxEncodedPayload {
		file.Close()
		return nil, errors.New("pairing bundle is invalid")
	}
	var bundle Bundle
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&bundle); err != nil {
		file.Close()
		return nil, errors.New("pairing bundle is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		file.Close()
		return nil, errors.New("pairing bundle is invalid")
	}
	if _, err := bundle.Endpoint(); err != nil {
		file.Close()
		return nil, err
	}
	return &Opened{Bundle: bundle, path: path, file: file}, nil
}

func (o *Opened) Remove() error {
	if o == nil || o.file == nil {
		return errors.New("pairing bundle is not open")
	}
	openedInfo, err := o.file.Stat()
	if err != nil {
		return errors.New("inspect open pairing bundle")
	}
	pathInfo, err := os.Lstat(o.path)
	if err != nil || pathInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(openedInfo, pathInfo) {
		return errors.New("pairing bundle changed while open")
	}
	if err := unix.Unlink(o.path); err != nil {
		return errors.New("remove pairing bundle")
	}
	if err := syncDirectory(filepath.Dir(o.path)); err != nil {
		return errors.New("sync pairing bundle directory")
	}
	return o.Close()
}

func (o *Opened) Discard(now time.Time) error {
	if o == nil {
		return errors.New("pairing bundle is not open")
	}
	if now.Before(o.Bundle.ExpiresAt) {
		return ErrStillValid
	}
	return o.Remove()
}

func (o *Opened) Close() error {
	if o == nil || o.file == nil {
		return nil
	}
	err := o.file.Close()
	o.file = nil
	return err
}

func validateBundle(bundle Bundle) error {
	if bundle.Version != Version {
		return errors.New("pairing bundle version is unsupported")
	}
	requestID, err := uuid.Parse(bundle.RequestID)
	if err != nil || requestID.String() != bundle.RequestID {
		return errors.New("pairing bundle request id is invalid")
	}
	token, err := base64.RawURLEncoding.DecodeString(bundle.PairingToken)
	if err != nil || len(token) != 32 || base64.RawURLEncoding.EncodeToString(token) != bundle.PairingToken {
		return errors.New("pairing bundle token is invalid")
	}
	if bundle.ExpiresAt.IsZero() || bundle.ExpiresAt.Location() != time.UTC {
		return errors.New("pairing bundle expiry is invalid")
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
