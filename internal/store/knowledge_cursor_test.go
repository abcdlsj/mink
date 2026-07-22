package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"math"
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

func TestKnowledgeCursorKeyConcurrentOpenDoesNotClobber(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.db")
	database := openKnowledgeCursorStore(t, path)
	if _, err := database.db.Exec(`DELETE FROM knowledge_cursor_keys`); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	const opens = 8
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
	if strings.Contains(token, "principal") || strings.Contains(token, "query") || strings.Contains(token, "message") {
		t.Fatal("cursor token exposes payload text")
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
	return KnowledgeCursorSeekKey{Rank: -1.5, SourceKind: KnowledgeSourceMessage, SourceID: "00000000-0000-4000-8000-000000000001", RowID: 1}
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
