package store

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"time"

	workapp "github.com/abcdlsj/sumi/internal/work/application"
	"github.com/google/uuid"
)

const (
	workCursorTokenVersion = 1
	workCursorTokenMax     = 2048
	workCursorAAD          = "sumi-work-cursor-v1"

	workCursorPrincipalTag    = 1
	workCursorOrganizationTag = 2
	workCursorRootTag         = 3
	workCursorParentNullTag   = 4
	workCursorParentTag       = 5
	workCursorCreatedAtTag    = 6
	workCursorIDTag           = 7
)

var ErrWorkCursorUnavailable = workapp.ErrCursorUnavailable

type WorkCursorBinding struct {
	PrincipalFingerprint [sha256.Size]byte
	OrganizationID       string
}

type WorkCursorSeekKey struct {
	RootWorkID   string
	ParentWorkID string
	ParentIsNull bool
	CreatedAt    time.Time
	ID           string
}

func workCursorPrincipalFingerprint(principal Principal) [sha256.Size]byte {
	hash := sha256.New()
	writeWorkCursorField(hash, "kind", string(principal.Kind))
	writeWorkCursorField(hash, "id", principal.ID)
	writeWorkCursorField(hash, "organization", principal.OrganizationID)
	var value [sha256.Size]byte
	copy(value[:], hash.Sum(nil))
	return value
}

func writeWorkCursorField(writer io.Writer, name, value string) {
	_, _ = io.WriteString(writer, name)
	_, _ = writer.Write([]byte{0})
	_, _ = io.WriteString(writer, value)
	_, _ = writer.Write([]byte{0})
}

func (s *Store) SealWorkCursor(binding WorkCursorBinding, seek WorkCursorSeekKey) (string, error) {
	payload, err := marshalWorkCursor(binding, seek)
	if err != nil {
		return "", ErrWorkCursorUnavailable
	}
	nonce := make([]byte, s.cursorCodec.aead.NonceSize())
	if _, err := io.ReadFull(s.cursorCodec.random, nonce); err != nil {
		return "", ErrWorkCursorUnavailable
	}
	raw := make([]byte, 1+len(nonce))
	raw[0] = workCursorTokenVersion
	copy(raw[1:], nonce)
	raw = s.cursorCodec.aead.Seal(raw, nonce, payload, []byte(workCursorAAD))
	token := base64.RawURLEncoding.EncodeToString(raw)
	if len(token) > workCursorTokenMax {
		return "", ErrWorkCursorUnavailable
	}
	return token, nil
}

func (s *Store) OpenWorkCursor(token string, binding WorkCursorBinding) (WorkCursorSeekKey, error) {
	if len(token) == 0 || len(token) > workCursorTokenMax {
		return WorkCursorSeekKey{}, ErrWorkCursorUnavailable
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) < 1+s.cursorCodec.aead.NonceSize()+s.cursorCodec.aead.Overhead() || raw[0] != workCursorTokenVersion {
		return WorkCursorSeekKey{}, ErrWorkCursorUnavailable
	}
	nonce := raw[1 : 1+s.cursorCodec.aead.NonceSize()]
	payload, err := s.cursorCodec.aead.Open(nil, nonce, raw[1+s.cursorCodec.aead.NonceSize():], []byte(workCursorAAD))
	if err != nil {
		return WorkCursorSeekKey{}, ErrWorkCursorUnavailable
	}
	decodedBinding, seek, err := unmarshalWorkCursor(payload)
	if err != nil || decodedBinding != binding {
		return WorkCursorSeekKey{}, ErrWorkCursorUnavailable
	}
	return seek, nil
}

func marshalWorkCursor(binding WorkCursorBinding, seek WorkCursorSeekKey) ([]byte, error) {
	if !validWorkCursorBinding(binding) || !validWorkCursorSeekKey(seek) {
		return nil, errors.New("invalid work cursor")
	}
	payload := make([]byte, 0, 192)
	payload = appendWorkCursorField(payload, workCursorPrincipalTag, binding.PrincipalFingerprint[:])
	payload = appendWorkCursorField(payload, workCursorOrganizationTag, []byte(binding.OrganizationID))
	payload = appendWorkCursorField(payload, workCursorRootTag, []byte(seek.RootWorkID))
	parentNull := byte(0)
	if seek.ParentIsNull {
		parentNull = 1
	}
	payload = appendWorkCursorField(payload, workCursorParentNullTag, []byte{parentNull})
	payload = appendWorkCursorField(payload, workCursorParentTag, []byte(seek.ParentWorkID))
	payload = appendWorkCursorInt64(payload, workCursorCreatedAtTag, seek.CreatedAt.UnixNano())
	payload = appendWorkCursorField(payload, workCursorIDTag, []byte(seek.ID))
	return payload, nil
}

func appendWorkCursorField(payload []byte, tag byte, value []byte) []byte {
	payload = append(payload, tag, 0, 0)
	binary.BigEndian.PutUint16(payload[len(payload)-2:], uint16(len(value)))
	return append(payload, value...)
}

func appendWorkCursorInt64(payload []byte, tag byte, value int64) []byte {
	encoded := make([]byte, 8)
	binary.BigEndian.PutUint64(encoded, uint64(value))
	return appendWorkCursorField(payload, tag, encoded)
}

func unmarshalWorkCursor(payload []byte) (WorkCursorBinding, WorkCursorSeekKey, error) {
	var binding WorkCursorBinding
	var seek WorkCursorSeekKey
	seen := map[byte]bool{}
	for len(payload) > 0 {
		if len(payload) < 3 {
			return WorkCursorBinding{}, WorkCursorSeekKey{}, errors.New("truncated work cursor")
		}
		tag := payload[0]
		length := int(binary.BigEndian.Uint16(payload[1:3]))
		payload = payload[3:]
		if length > len(payload) || seen[tag] {
			return WorkCursorBinding{}, WorkCursorSeekKey{}, errors.New("invalid work cursor field")
		}
		seen[tag] = true
		value := payload[:length]
		payload = payload[length:]
		switch tag {
		case workCursorPrincipalTag:
			if len(value) != sha256.Size {
				return WorkCursorBinding{}, WorkCursorSeekKey{}, errors.New("invalid work cursor principal")
			}
			copy(binding.PrincipalFingerprint[:], value)
		case workCursorOrganizationTag:
			binding.OrganizationID = string(value)
		case workCursorRootTag:
			seek.RootWorkID = string(value)
		case workCursorParentNullTag:
			if len(value) != 1 || value[0] > 1 {
				return WorkCursorBinding{}, WorkCursorSeekKey{}, errors.New("invalid work cursor parent null")
			}
			seek.ParentIsNull = value[0] == 1
		case workCursorParentTag:
			seek.ParentWorkID = string(value)
		case workCursorCreatedAtTag:
			if len(value) != 8 {
				return WorkCursorBinding{}, WorkCursorSeekKey{}, errors.New("invalid work cursor timestamp")
			}
			seek.CreatedAt = time.Unix(0, int64(binary.BigEndian.Uint64(value))).UTC()
		case workCursorIDTag:
			seek.ID = string(value)
		default:
			return WorkCursorBinding{}, WorkCursorSeekKey{}, errors.New("unknown work cursor field")
		}
	}
	if len(seen) != 7 || !validWorkCursorBinding(binding) || !validWorkCursorSeekKey(seek) {
		return WorkCursorBinding{}, WorkCursorSeekKey{}, errors.New("incomplete work cursor")
	}
	return binding, seek, nil
}

func validWorkCursorBinding(binding WorkCursorBinding) bool {
	return binding.PrincipalFingerprint != [sha256.Size]byte{} && canonicalWorkCursorID(binding.OrganizationID)
}

func validWorkCursorSeekKey(seek WorkCursorSeekKey) bool {
	if !canonicalWorkCursorID(seek.RootWorkID) || !canonicalWorkCursorID(seek.ID) || seek.CreatedAt.IsZero() {
		return false
	}
	return (seek.ParentIsNull && seek.ParentWorkID == "") || (!seek.ParentIsNull && canonicalWorkCursorID(seek.ParentWorkID))
}

func canonicalWorkCursorID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && len(value) == 36 && parsed.String() == value
}
