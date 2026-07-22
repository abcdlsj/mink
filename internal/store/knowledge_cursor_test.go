package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestKnowledgeCursorKeyPersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.db")
	first := openKnowledgeCursorStore(t, path)
	firstKey := readKnowledgeCursorKey(t, first)
	binding, seek := testKnowledgeCursorBinding(), testKnowledgeCursorSeekKey()
	cursor, err := first.SealKnowledgeCursor(binding, seek)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second := openKnowledgeCursorStore(t, path)
	defer second.Close()
	if got := readKnowledgeCursorKey(t, second); !bytes.Equal(got, firstKey) {
		t.Fatal("knowledge cursor key changed after reopen")
	}
	if got, err := second.OpenKnowledgeCursor(cursor, binding); err != nil || got != seek {
		t.Fatalf("cursor after reopen = %+v, %v", got, err)
	}
}

func TestOpenSecuresLiveSQLiteFilesAndKeepsCursorKeyQuiet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.db")
	store := openKnowledgeCursorStore(t, path)
	defer store.Close()
	key := readKnowledgeCursorKey(t, store)
	for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
		info, err := os.Stat(candidate)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("live sqlite file %s mode = %v, %v", candidate, info.Mode().Perm(), err)
		}
	}
	wal, err := os.ReadFile(path + "-wal")
	if err != nil || !bytes.Contains(wal, key) {
		t.Fatalf("live WAL does not contain the canonical cursor key: %v", err)
	}
}

func TestOpenFailsClosedForCursorKeyRandomFailureAndCorruption(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.db")
	failed, err := openWithRandomReader(path, failingReader{})
	if failed != nil || !errors.Is(err, ErrKnowledgeCursorKeyUnavailable) {
		t.Fatalf("Open with random failure = %v, %v", failed, err)
	}
	store := openKnowledgeCursorStore(t, path)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if err := configure(raw); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	if _, err := raw.Exec(`PRAGMA ignore_check_constraints = ON`); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	if _, err := raw.Exec(`UPDATE knowledge_cursor_keys SET key = X'01' WHERE singleton = 1`); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		failed, err = Open(path)
		if failed != nil || !errors.Is(err, ErrKnowledgeCursorKeyUnavailable) {
			t.Fatalf("Open with corrupt key = %v, %v", failed, err)
		}
	}
}

func TestKnowledgeCursorKeyConcurrentOpenDoesNotClobber(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.db")
	database := openKnowledgeCursorStore(t, path)
	if _, err := database.db.Exec(`DELETE FROM knowledge_cursor_keys`); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	const opens = 10
	keys := make(chan []byte, opens)
	errs := make(chan error, opens)
	var group sync.WaitGroup
	for range opens {
		group.Add(1)
		go func() {
			defer group.Done()
			store, err := Open(path)
			if err != nil {
				errs <- err
				return
			}
			var key []byte
			err = store.db.QueryRow(`SELECT key FROM knowledge_cursor_keys WHERE singleton = 1`).Scan(&key)
			if closeErr := store.Close(); err == nil {
				err = closeErr
			}
			errs <- err
			if err == nil {
				keys <- key
			}
		}()
	}
	group.Wait()
	close(errs)
	close(keys)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var want []byte
	for key := range keys {
		if want == nil {
			want = key
		} else if !bytes.Equal(want, key) {
			t.Fatal("concurrent Open returned different cursor keys")
		}
	}
	if len(want) != 32 {
		t.Fatalf("cursor key length = %d", len(want))
	}
}

func TestKnowledgeCursorKeyConcurrentOpenDifferentDatabases(t *testing.T) {
	const opens = 10
	paths := make([]string, opens)
	for index := range paths {
		paths[index] = filepath.Join(t.TempDir(), fmt.Sprintf("server-%d.db", index))
	}
	keys := make(chan []byte, opens)
	errs := make(chan error, opens)
	var group sync.WaitGroup
	for index := range opens {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			store, err := Open(paths[index])
			if err != nil {
				errs <- err
				return
			}
			var key []byte
			err = store.db.QueryRow(`SELECT key FROM knowledge_cursor_keys WHERE singleton = 1`).Scan(&key)
			if closeErr := store.Close(); err == nil {
				err = closeErr
			}
			errs <- err
			if err == nil {
				keys <- key
			}
		}(index)
	}
	group.Wait()
	close(errs)
	close(keys)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for key := range keys {
		if len(key) != 32 {
			t.Fatalf("cursor key length = %d", len(key))
		}
	}
}

func TestBootstrapKnowledgeCursorKeyFailsClosed(t *testing.T) {
	stages := []struct {
		name  string
		store knowledgeCursorKeyStore
	}{
		{"begin", fakeKnowledgeCursorKeyStore{beginErr: errors.New("begin")}},
		{"insert", fakeKnowledgeCursorKeyStore{transaction: fakeKnowledgeCursorKeyTransaction{insertErr: errors.New("insert")}}},
		{"read", fakeKnowledgeCursorKeyStore{transaction: fakeKnowledgeCursorKeyTransaction{readErr: errors.New("read")}}},
		{"length", fakeKnowledgeCursorKeyStore{transaction: fakeKnowledgeCursorKeyTransaction{key: []byte{1}}}},
		{"commit", fakeKnowledgeCursorKeyStore{transaction: fakeKnowledgeCursorKeyTransaction{key: bytes.Repeat([]byte{1}, 32), commitErr: errors.New("commit")}}},
	}
	for _, stage := range stages {
		t.Run(stage.name, func(t *testing.T) {
			_, err := bootstrapKnowledgeCursorKeyWithStore(context.Background(), stage.store, bytes.NewReader(bytes.Repeat([]byte{1}, 32)))
			if !errors.Is(err, ErrKnowledgeCursorKeyUnavailable) {
				t.Fatalf("bootstrap error = %v", err)
			}
		})
	}
	_, err := bootstrapKnowledgeCursorKeyWithStore(context.Background(), fakeKnowledgeCursorKeyStore{}, failingReader{})
	if !errors.Is(err, ErrKnowledgeCursorKeyUnavailable) {
		t.Fatalf("random bootstrap error = %v", err)
	}
}

func TestKnowledgeCursorCodecRejectsInvalidOrMismatchedTokens(t *testing.T) {
	key := [32]byte{1}
	codec, err := newKnowledgeCursorCodec(key, bytes.NewReader(bytes.Repeat([]byte{7}, 24)))
	if err != nil {
		t.Fatal(err)
	}
	binding, seek := testKnowledgeCursorBinding(), testKnowledgeCursorSeekKey()
	token, err := codec.seal(binding, seek)
	if err != nil {
		t.Fatal(err)
	}
	assertKnowledgeCursorConfidential(t, token, binding, seek)
	payload := marshalCursorForTest(t, binding, seek)
	plaintext := append([]byte{knowledgeCursorTokenVersion}, bytes.Repeat([]byte{5}, knowledgeCursorNonceSize)...)
	plaintext = append(plaintext, payload...)
	if err := knowledgeCursorTokenConfidential(base64.RawURLEncoding.EncodeToString(plaintext), binding, seek); err == nil {
		t.Fatal("confidentiality oracle accepted plaintext payload")
	}
	if got, err := codec.open(token, binding); err != nil || got != seek {
		t.Fatalf("cursor round-trip = %+v, %v", got, err)
	}
	wrongKey, err := newKnowledgeCursorCodec([32]byte{2}, bytes.NewReader(nil))
	if err != nil {
		t.Fatal(err)
	}
	wrongBinding := binding
	wrongBinding.Generation++
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatal(err)
	}
	wrongVersion := append([]byte(nil), raw...)
	wrongVersion[0]++
	tampered := append([]byte(nil), raw...)
	tampered[len(tampered)-1] ^= 1
	for _, candidate := range []string{token[:len(token)-1], token + "A", "A" + token, base64.RawURLEncoding.EncodeToString(wrongVersion), base64.RawURLEncoding.EncodeToString(tampered), strings.Repeat("A", knowledgeCursorTokenMax+1)} {
		if _, err := codec.open(candidate, binding); !errors.Is(err, ErrKnowledgeCursorUnavailable) {
			t.Fatalf("invalid cursor error = %v", err)
		}
	}
	if _, err := wrongKey.open(token, binding); !errors.Is(err, ErrKnowledgeCursorUnavailable) {
		t.Fatalf("wrong key error = %v", err)
	}
	if _, err := codec.open(token, wrongBinding); !errors.Is(err, ErrKnowledgeCursorUnavailable) {
		t.Fatalf("wrong binding error = %v", err)
	}
	wrongBinding = binding
	wrongBinding.QueryHash[0]++
	if _, err := codec.open(token, wrongBinding); !errors.Is(err, ErrKnowledgeCursorUnavailable) {
		t.Fatalf("wrong query binding error = %v", err)
	}
}

func TestKnowledgeCursorCodecUsesFreshNonceAndRejectsMalformedPayload(t *testing.T) {
	key := [32]byte{3}
	nonces := append(bytes.Repeat([]byte{9}, 12), bytes.Repeat([]byte{10}, 12)...)
	codec, err := newKnowledgeCursorCodec(key, bytes.NewReader(nonces))
	if err != nil {
		t.Fatal(err)
	}
	binding, seek := testKnowledgeCursorBinding(), testKnowledgeCursorSeekKey()
	first, err := codec.seal(binding, seek)
	if err != nil {
		t.Fatal(err)
	}
	second, err := codec.seal(binding, seek)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("cursor tokens reused nonce")
	}
	for _, malformed := range []KnowledgeCursorSeekKey{{Rank: math.NaN(), SourceKind: KnowledgeSourceMessage, SourceID: seek.SourceID, RowID: 1}, {Rank: math.Inf(1), SourceKind: KnowledgeSourceMessage, SourceID: seek.SourceID, RowID: 1}} {
		if _, err := codec.seal(binding, malformed); !errors.Is(err, ErrKnowledgeCursorUnavailable) {
			t.Fatalf("non-finite cursor error = %v", err)
		}
	}
	for _, payload := range [][]byte{append(marshalCursorForTest(t, binding, seek), 0), appendKnowledgeCursorField(marshalCursorForTest(t, binding, seek), knowledgeCursorKindTag, []byte(KnowledgeSourceMessage)), appendKnowledgeCursorField([]byte{}, 99, []byte("unknown"))} {
		if _, err := openKnowledgeCursorPayload(codec, payload, binding); !errors.Is(err, ErrKnowledgeCursorUnavailable) {
			t.Fatalf("malformed payload error = %v", err)
		}
	}
}

func TestKnowledgeCursorRejectsNonCanonicalSourceIDs(t *testing.T) {
	codec, err := newKnowledgeCursorCodec([32]byte{4}, bytes.NewReader(bytes.Repeat([]byte{1}, 128)))
	if err != nil {
		t.Fatal(err)
	}
	binding, seek := testKnowledgeCursorBinding(), testKnowledgeCursorSeekKey()
	for _, id := range []string{
		"urn:uuid:" + seek.SourceID,
		"{" + seek.SourceID + "}",
		strings.ReplaceAll(seek.SourceID, "-", ""),
		strings.ToUpper(seek.SourceID),
	} {
		seek.SourceID = id
		if _, err := codec.seal(binding, seek); !errors.Is(err, ErrKnowledgeCursorUnavailable) {
			t.Fatalf("non-canonical source id %q error = %v", id, err)
		}
	}
}

func openKnowledgeCursorStore(t *testing.T, path string) *Store {
	t.Helper()
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func readKnowledgeCursorKey(t *testing.T, store *Store) []byte {
	t.Helper()
	var key []byte
	if err := store.db.QueryRow(`SELECT key FROM knowledge_cursor_keys WHERE singleton = 1`).Scan(&key); err != nil {
		t.Fatal(err)
	}
	return key
}

func testKnowledgeCursorBinding() KnowledgeCursorBinding {
	return KnowledgeCursorBinding{PrincipalFingerprint: sha256.Sum256([]byte("principal")), QueryHash: sha256.Sum256([]byte("query")), Generation: 1}
}

func testKnowledgeCursorSeekKey() KnowledgeCursorSeekKey {
	return KnowledgeCursorSeekKey{Rank: -1.5, SourceKind: KnowledgeSourceArtifactVersion, SourceID: "123e4567-e89b-42d3-a456-426614174000", SourceVersion: 7, RowID: 97}
}

func assertKnowledgeCursorConfidential(t *testing.T, token string, binding KnowledgeCursorBinding, seek KnowledgeCursorSeekKey) {
	t.Helper()
	if err := knowledgeCursorTokenConfidential(token, binding, seek); err != nil {
		t.Fatal(err)
	}
}

func knowledgeCursorTokenConfidential(token string, binding KnowledgeCursorBinding, seek KnowledgeCursorSeekKey) error {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return err
	}
	values := [][]byte{binding.PrincipalFingerprint[:], binding.QueryHash[:], []byte(seek.SourceKind), []byte(seek.SourceID), cursorUint64(binding.Generation), cursorUint64(math.Float64bits(seek.Rank)), cursorUint64(seek.SourceVersion), cursorUint64(uint64(seek.RowID))}
	for _, value := range values {
		if bytes.Contains(raw, value) {
			return errors.New("cursor raw token exposes payload")
		}
	}
	return nil
}

func cursorUint64(value uint64) []byte {
	encoded := make([]byte, 8)
	binary.BigEndian.PutUint64(encoded, value)
	return encoded
}

func marshalCursorForTest(t *testing.T, binding KnowledgeCursorBinding, seek KnowledgeCursorSeekKey) []byte {
	t.Helper()
	payload, err := marshalKnowledgeCursor(binding, seek)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func openKnowledgeCursorPayload(codec knowledgeCursorCodec, payload []byte, binding KnowledgeCursorBinding) (KnowledgeCursorSeekKey, error) {
	nonce := bytes.Repeat([]byte{5}, knowledgeCursorNonceSize)
	raw := append([]byte{knowledgeCursorTokenVersion}, nonce...)
	raw = codec.aead.Seal(raw, nonce, payload, []byte(knowledgeCursorAAD))
	return codec.open(base64.RawURLEncoding.EncodeToString(raw), binding)
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

type fakeKnowledgeCursorKeyStore struct {
	beginErr    error
	transaction fakeKnowledgeCursorKeyTransaction
}

func (store fakeKnowledgeCursorKeyStore) beginKnowledgeCursorKey(context.Context) (knowledgeCursorKeyTransaction, error) {
	if store.beginErr != nil {
		return nil, store.beginErr
	}
	return &store.transaction, nil
}

type fakeKnowledgeCursorKeyTransaction struct {
	insertErr, readErr, commitErr error
	key                           []byte
}

func (tx *fakeKnowledgeCursorKeyTransaction) insertKnowledgeCursorKey(context.Context, []byte) error {
	return tx.insertErr
}
func (tx *fakeKnowledgeCursorKeyTransaction) readKnowledgeCursorKey(context.Context) ([]byte, error) {
	return tx.key, tx.readErr
}
func (tx *fakeKnowledgeCursorKeyTransaction) Commit() error   { return tx.commitErr }
func (tx *fakeKnowledgeCursorKeyTransaction) Rollback() error { return nil }
