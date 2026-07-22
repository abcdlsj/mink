package store

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"

	"github.com/google/uuid"
)

const (
	knowledgeCursorTokenVersion = 1
	knowledgeCursorNonceSize    = 12
	knowledgeCursorTokenMax     = 2048
	knowledgeCursorAAD          = "sumi-knowledge-cursor-v1"

	knowledgeCursorPrincipalTag  = 1
	knowledgeCursorQueryTag      = 2
	knowledgeCursorGenerationTag = 3
	knowledgeCursorRankTag       = 4
	knowledgeCursorKindTag       = 5
	knowledgeCursorIDTag         = 6
	knowledgeCursorVersionTag    = 7
	knowledgeCursorRowIDTag      = 8
)

var (
	ErrKnowledgeCursorKeyUnavailable = errors.New("knowledge cursor key unavailable")
	ErrKnowledgeCursorUnavailable    = errors.New("knowledge cursor unavailable")
)

type KnowledgeCursorBinding struct {
	PrincipalFingerprint [sha256.Size]byte
	QueryHash            [sha256.Size]byte
	Generation           uint64
}

type KnowledgeCursorSeekKey struct {
	Rank          float64
	SourceKind    string
	SourceID      string
	SourceVersion uint64
	RowID         int64
}

type knowledgeCursorCodec struct {
	aead   cipher.AEAD
	random io.Reader
}

func newKnowledgeCursorCodec(key [32]byte, random io.Reader) (knowledgeCursorCodec, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return knowledgeCursorCodec{}, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return knowledgeCursorCodec{}, err
	}
	if aead.NonceSize() != knowledgeCursorNonceSize || random == nil {
		return knowledgeCursorCodec{}, errors.New("invalid knowledge cursor codec")
	}
	return knowledgeCursorCodec{aead: aead, random: random}, nil
}

func (s *Store) SealKnowledgeCursor(binding KnowledgeCursorBinding, seek KnowledgeCursorSeekKey) (string, error) {
	return s.knowledgeCursorCodec.seal(binding, seek)
}

func (s *Store) OpenKnowledgeCursor(token string, binding KnowledgeCursorBinding) (KnowledgeCursorSeekKey, error) {
	return s.knowledgeCursorCodec.open(token, binding)
}

func (codec knowledgeCursorCodec) seal(binding KnowledgeCursorBinding, seek KnowledgeCursorSeekKey) (string, error) {
	payload, err := marshalKnowledgeCursor(binding, seek)
	if err != nil {
		return "", ErrKnowledgeCursorUnavailable
	}
	nonce := make([]byte, knowledgeCursorNonceSize)
	if _, err := io.ReadFull(codec.random, nonce); err != nil {
		return "", ErrKnowledgeCursorUnavailable
	}
	raw := make([]byte, 1+len(nonce))
	raw[0] = knowledgeCursorTokenVersion
	copy(raw[1:], nonce)
	raw = codec.aead.Seal(raw, nonce, payload, []byte(knowledgeCursorAAD))
	token := base64.RawURLEncoding.EncodeToString(raw)
	if len(token) > knowledgeCursorTokenMax {
		return "", ErrKnowledgeCursorUnavailable
	}
	return token, nil
}

func (codec knowledgeCursorCodec) open(token string, binding KnowledgeCursorBinding) (KnowledgeCursorSeekKey, error) {
	if len(token) == 0 || len(token) > knowledgeCursorTokenMax {
		return KnowledgeCursorSeekKey{}, ErrKnowledgeCursorUnavailable
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) < 1+knowledgeCursorNonceSize+codec.aead.Overhead() || raw[0] != knowledgeCursorTokenVersion {
		return KnowledgeCursorSeekKey{}, ErrKnowledgeCursorUnavailable
	}
	nonce := raw[1 : 1+knowledgeCursorNonceSize]
	payload, err := codec.aead.Open(nil, nonce, raw[1+knowledgeCursorNonceSize:], []byte(knowledgeCursorAAD))
	if err != nil {
		return KnowledgeCursorSeekKey{}, ErrKnowledgeCursorUnavailable
	}
	decodedBinding, seek, err := unmarshalKnowledgeCursor(payload)
	if err != nil || decodedBinding != binding {
		return KnowledgeCursorSeekKey{}, ErrKnowledgeCursorUnavailable
	}
	return seek, nil
}

func marshalKnowledgeCursor(binding KnowledgeCursorBinding, seek KnowledgeCursorSeekKey) ([]byte, error) {
	if binding.Generation == 0 || !validKnowledgeCursorSeekKey(seek) {
		return nil, errors.New("invalid knowledge cursor")
	}
	payload := make([]byte, 0, 192)
	payload = appendKnowledgeCursorField(payload, knowledgeCursorPrincipalTag, binding.PrincipalFingerprint[:])
	payload = appendKnowledgeCursorField(payload, knowledgeCursorQueryTag, binding.QueryHash[:])
	payload = appendKnowledgeCursorUint64(payload, knowledgeCursorGenerationTag, binding.Generation)
	payload = appendKnowledgeCursorUint64(payload, knowledgeCursorRankTag, math.Float64bits(seek.Rank))
	payload = appendKnowledgeCursorField(payload, knowledgeCursorKindTag, []byte(seek.SourceKind))
	payload = appendKnowledgeCursorField(payload, knowledgeCursorIDTag, []byte(seek.SourceID))
	payload = appendKnowledgeCursorUint64(payload, knowledgeCursorVersionTag, seek.SourceVersion)
	payload = appendKnowledgeCursorUint64(payload, knowledgeCursorRowIDTag, uint64(seek.RowID))
	return payload, nil
}

func appendKnowledgeCursorField(payload []byte, tag byte, value []byte) []byte {
	payload = append(payload, tag, 0, 0)
	binary.BigEndian.PutUint16(payload[len(payload)-2:], uint16(len(value)))
	return append(payload, value...)
}

func appendKnowledgeCursorUint64(payload []byte, tag byte, value uint64) []byte {
	encoded := make([]byte, 8)
	binary.BigEndian.PutUint64(encoded, value)
	return appendKnowledgeCursorField(payload, tag, encoded)
}

func unmarshalKnowledgeCursor(payload []byte) (KnowledgeCursorBinding, KnowledgeCursorSeekKey, error) {
	var binding KnowledgeCursorBinding
	var seek KnowledgeCursorSeekKey
	seen := map[byte]bool{}
	for len(payload) > 0 {
		if len(payload) < 3 {
			return KnowledgeCursorBinding{}, KnowledgeCursorSeekKey{}, errors.New("truncated knowledge cursor")
		}
		tag := payload[0]
		length := int(binary.BigEndian.Uint16(payload[1:3]))
		payload = payload[3:]
		if length > len(payload) || seen[tag] {
			return KnowledgeCursorBinding{}, KnowledgeCursorSeekKey{}, errors.New("invalid knowledge cursor field")
		}
		seen[tag] = true
		value := payload[:length]
		payload = payload[length:]
		switch tag {
		case knowledgeCursorPrincipalTag:
			if len(value) != sha256.Size {
				return KnowledgeCursorBinding{}, KnowledgeCursorSeekKey{}, errors.New("invalid cursor principal")
			}
			copy(binding.PrincipalFingerprint[:], value)
		case knowledgeCursorQueryTag:
			if len(value) != sha256.Size {
				return KnowledgeCursorBinding{}, KnowledgeCursorSeekKey{}, errors.New("invalid cursor query")
			}
			copy(binding.QueryHash[:], value)
		case knowledgeCursorGenerationTag:
			value, err := readKnowledgeCursorUint64(value)
			if err != nil {
				return KnowledgeCursorBinding{}, KnowledgeCursorSeekKey{}, err
			}
			binding.Generation = value
		case knowledgeCursorRankTag:
			value, err := readKnowledgeCursorUint64(value)
			if err != nil {
				return KnowledgeCursorBinding{}, KnowledgeCursorSeekKey{}, err
			}
			seek.Rank = math.Float64frombits(value)
		case knowledgeCursorKindTag:
			seek.SourceKind = string(value)
		case knowledgeCursorIDTag:
			seek.SourceID = string(value)
		case knowledgeCursorVersionTag:
			value, err := readKnowledgeCursorUint64(value)
			if err != nil {
				return KnowledgeCursorBinding{}, KnowledgeCursorSeekKey{}, err
			}
			seek.SourceVersion = value
		case knowledgeCursorRowIDTag:
			value, err := readKnowledgeCursorUint64(value)
			if err != nil {
				return KnowledgeCursorBinding{}, KnowledgeCursorSeekKey{}, err
			}
			seek.RowID = int64(value)
		default:
			return KnowledgeCursorBinding{}, KnowledgeCursorSeekKey{}, errors.New("unknown knowledge cursor field")
		}
	}
	if len(seen) != 8 || binding.Generation == 0 || !validKnowledgeCursorSeekKey(seek) {
		return KnowledgeCursorBinding{}, KnowledgeCursorSeekKey{}, errors.New("incomplete knowledge cursor")
	}
	return binding, seek, nil
}

func readKnowledgeCursorUint64(value []byte) (uint64, error) {
	if len(value) != 8 {
		return 0, errors.New("invalid cursor integer")
	}
	return binary.BigEndian.Uint64(value), nil
}

func validKnowledgeCursorSeekKey(seek KnowledgeCursorSeekKey) bool {
	if math.IsNaN(seek.Rank) || math.IsInf(seek.Rank, 0) || seek.RowID <= 0 || uuid.Validate(seek.SourceID) != nil {
		return false
	}
	if seek.SourceKind == KnowledgeSourceMessage || seek.SourceKind == KnowledgeSourceWork {
		return seek.SourceVersion == 0
	}
	return seek.SourceKind == KnowledgeSourceArtifactVersion && seek.SourceVersion > 0
}

func bootstrapKnowledgeCursorKey(ctx context.Context, db *sql.DB, random io.Reader) ([32]byte, error) {
	return bootstrapKnowledgeCursorKeyWithStore(ctx, sqlKnowledgeCursorKeyStore{db: db}, random)
}

type knowledgeCursorKeyStore interface {
	beginKnowledgeCursorKey(context.Context) (knowledgeCursorKeyTransaction, error)
}
type knowledgeCursorKeyTransaction interface {
	insertKnowledgeCursorKey(context.Context, []byte) error
	readKnowledgeCursorKey(context.Context) ([]byte, error)
	Commit() error
	Rollback() error
}

func bootstrapKnowledgeCursorKeyWithStore(ctx context.Context, store knowledgeCursorKeyStore, random io.Reader) ([32]byte, error) {
	var candidate [32]byte
	if _, err := io.ReadFull(random, candidate[:]); err != nil {
		return [32]byte{}, fmt.Errorf("initialize knowledge cursor key: %w", ErrKnowledgeCursorKeyUnavailable)
	}
	tx, err := store.beginKnowledgeCursorKey(ctx)
	if err != nil {
		return [32]byte{}, fmt.Errorf("initialize knowledge cursor key: %w", ErrKnowledgeCursorKeyUnavailable)
	}
	defer tx.Rollback()
	if err := tx.insertKnowledgeCursorKey(ctx, candidate[:]); err != nil {
		return [32]byte{}, fmt.Errorf("initialize knowledge cursor key: %w", ErrKnowledgeCursorKeyUnavailable)
	}
	raw, err := tx.readKnowledgeCursorKey(ctx)
	if err != nil || len(raw) != len(candidate) {
		return [32]byte{}, fmt.Errorf("initialize knowledge cursor key: %w", ErrKnowledgeCursorKeyUnavailable)
	}
	var key [32]byte
	copy(key[:], raw)
	if err := tx.Commit(); err != nil {
		return [32]byte{}, fmt.Errorf("initialize knowledge cursor key: %w", ErrKnowledgeCursorKeyUnavailable)
	}
	return key, nil
}

type sqlKnowledgeCursorKeyStore struct{ db *sql.DB }

func (store sqlKnowledgeCursorKeyStore) beginKnowledgeCursorKey(ctx context.Context) (knowledgeCursorKeyTransaction, error) {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	return sqlKnowledgeCursorKeyTransaction{tx: tx}, nil
}

type sqlKnowledgeCursorKeyTransaction struct{ tx *sql.Tx }

func (tx sqlKnowledgeCursorKeyTransaction) insertKnowledgeCursorKey(ctx context.Context, key []byte) error {
	_, err := tx.tx.ExecContext(ctx, `INSERT INTO knowledge_cursor_keys(singleton, key) VALUES(1, ?) ON CONFLICT(singleton) DO NOTHING`, key)
	return err
}
func (tx sqlKnowledgeCursorKeyTransaction) readKnowledgeCursorKey(ctx context.Context) ([]byte, error) {
	var key []byte
	err := tx.tx.QueryRowContext(ctx, `SELECT key FROM knowledge_cursor_keys WHERE singleton = 1`).Scan(&key)
	return key, err
}
func (tx sqlKnowledgeCursorKeyTransaction) Commit() error   { return tx.tx.Commit() }
func (tx sqlKnowledgeCursorKeyTransaction) Rollback() error { return tx.tx.Rollback() }
