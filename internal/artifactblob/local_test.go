package artifactblob

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestLocalPutOpenStreamsAndDeduplicates(t *testing.T) {
	local := openTestLocal(t)
	payload := bytes.Repeat([]byte("artifact-stream-"), 10000)
	source := &boundedReader{reader: bytes.NewReader(payload)}
	digest, size, err := local.Put(context.Background(), source, int64(len(payload)))
	if err != nil {
		t.Fatal(err)
	}
	if size != int64(len(payload)) || digest != sha256.Sum256(payload) {
		t.Fatalf("put = %x/%d", digest, size)
	}
	if source.maxRequest > MaxChunkSize {
		t.Fatalf("largest source read = %d, want <= %d", source.maxRequest, MaxChunkSize)
	}

	secondDigest, secondSize, err := local.Put(context.Background(), bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		t.Fatal(err)
	}
	if secondDigest != digest || secondSize != size {
		t.Fatalf("deduplicated put = %x/%d", secondDigest, secondSize)
	}
	directory, path := local.objectPath(digest)
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(path) {
		t.Fatalf("object entries = %v", entries)
	}
	assertMode(t, local.root, 0o700)
	assertMode(t, local.objects, 0o700)
	assertMode(t, local.staging, 0o700)
	assertMode(t, local.quarantine, 0o700)
	assertMode(t, directory, 0o700)
	assertMode(t, path, 0o600)
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !hasSingleLink(info) {
		t.Fatal("published artifact did not settle to one filesystem link")
	}

	content, err := local.Open(context.Background(), digest, size)
	if err != nil {
		t.Fatal(err)
	}
	defer content.Close()
	got, err := io.ReadAll(content)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("opened content differs")
	}
	reopened, err := OpenLocal(local.root)
	if err != nil {
		t.Fatal(err)
	}
	restartedContent, err := reopened.Open(context.Background(), digest, size)
	if err != nil {
		t.Fatal(err)
	}
	restartedPayload, err := io.ReadAll(restartedContent)
	restartedContent.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(restartedPayload, payload) {
		t.Fatal("restarted artifact content differs")
	}
}

func TestLocalPutBoundariesCancellationAndNoProgress(t *testing.T) {
	local := openTestLocal(t)
	emptyDigest, size, err := local.Put(context.Background(), bytes.NewReader(nil), 1)
	if err != nil {
		t.Fatal(err)
	}
	if size != 0 || emptyDigest != sha256.Sum256(nil) {
		t.Fatalf("empty put = %x/%d", emptyDigest, size)
	}
	if _, _, err := local.Put(context.Background(), bytes.NewReader([]byte("12345678")), 8); err != nil {
		t.Fatalf("exact limit: %v", err)
	}
	if _, _, err := local.Put(context.Background(), bytes.NewReader([]byte("123456789")), 8); !errors.Is(err, ErrBlobTooLarge) {
		t.Fatalf("limit + 1 = %v", err)
	}
	if _, _, err := local.Put(context.Background(), bytes.NewReader(nil), MaxBlobSize+1); err == nil {
		t.Fatal("accepted a limit above the hard maximum")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := local.Put(canceled, bytes.NewReader([]byte("x")), 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled put = %v", err)
	}
	if _, _, err := local.Put(context.Background(), zeroReader{}, 1); err == nil {
		t.Fatal("accepted a reader that makes no progress")
	}
	entries, err := os.ReadDir(local.staging)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("staging entries after failures = %d", len(entries))
	}
}

func TestLocalHardMaximum(t *testing.T) {
	local := openTestLocal(t)
	digest, size, err := local.Put(context.Background(), io.LimitReader(zeroStream{}, MaxBlobSize), MaxBlobSize)
	if err != nil {
		t.Fatal(err)
	}
	if size != MaxBlobSize || digest == ([sha256.Size]byte{}) {
		t.Fatalf("maximum put = %x/%d", digest, size)
	}
	if _, _, err := local.Put(context.Background(), io.LimitReader(zeroStream{}, MaxBlobSize+1), MaxBlobSize); !errors.Is(err, ErrBlobTooLarge) {
		t.Fatalf("hard maximum + 1 = %v", err)
	}
	entries, err := os.ReadDir(local.staging)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("staging entries after hard maximum failure = %d", len(entries))
	}
}

func TestLocalOpenRejectsMissingSymlinkModeAndCorruption(t *testing.T) {
	local := openTestLocal(t)
	payload := []byte("verified artifact")
	digest, size, err := local.Put(context.Background(), bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		t.Fatal(err)
	}
	_, path := local.objectPath(digest)
	externalLink := filepath.Join(t.TempDir(), "external-link")
	if err := os.Link(path, externalLink); err != nil {
		t.Fatal(err)
	}
	if _, err := local.Open(context.Background(), digest, size); !errors.Is(err, ErrBlobIntegrity) {
		t.Fatalf("multiple-link open = %v", err)
	}
	if err := os.Remove(externalLink); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := local.Open(context.Background(), digest, size); !errors.Is(err, ErrBlobIntegrity) {
		t.Fatalf("wrong mode open = %v", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("tampered artifact"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := local.Open(context.Background(), digest, size); !errors.Is(err, ErrBlobIntegrity) {
		t.Fatalf("corrupt open = %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err := local.Open(context.Background(), digest, size); !errors.Is(err, ErrBlobIntegrity) {
		t.Fatalf("symlink open = %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := local.Open(context.Background(), digest, size); !errors.Is(err, ErrBlobMissing) {
		t.Fatalf("missing open = %v", err)
	}
}

func TestLocalReconcileQuarantinesMultipleLinkObject(t *testing.T) {
	local := openTestLocal(t)
	payload := []byte("linked artifact")
	digest, size, err := local.Put(context.Background(), bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		t.Fatal(err)
	}
	_, path := local.objectPath(digest)
	externalLink := filepath.Join(t.TempDir(), "external-link")
	if err := os.Link(path, externalLink); err != nil {
		t.Fatal(err)
	}
	states, quarantined, _, err := local.Reconcile(
		context.Background(),
		map[[sha256.Size]byte]int64{digest: size},
		time.Date(2026, 7, 21, 16, 0, 0, 0, time.UTC),
		24*time.Hour,
	)
	if err != nil {
		t.Fatal(err)
	}
	if states[digest] != string(IntegrityCorrupt) || quarantined != 1 {
		t.Fatalf("multiple-link reconcile = %v/%d", states, quarantined)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("multiple-link canonical object remains: %v", err)
	}
	if content, err := os.ReadFile(externalLink); err != nil || !bytes.Equal(content, payload) {
		t.Fatalf("external inode = %q, %v", content, err)
	}
}

func TestLocalReconcileCleansStagesAndQuarantinesWithoutEarlyGC(t *testing.T) {
	local := openTestLocal(t)
	now := time.Date(2026, 7, 21, 15, 0, 0, 0, time.UTC)
	missingDigest := sha256.Sum256([]byte("missing"))
	corruptPayload := []byte("corrupt source")
	corruptDigest, corruptSize, err := local.Put(context.Background(), bytes.NewReader(corruptPayload), int64(len(corruptPayload)))
	if err != nil {
		t.Fatal(err)
	}
	_, corruptPath := local.objectPath(corruptDigest)
	if err := os.WriteFile(corruptPath, []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := now.Add(-72 * time.Hour)
	if err := os.Chtimes(corruptPath, old, old); err != nil {
		t.Fatal(err)
	}
	orphanPayload := []byte("orphan")
	_, _, err = local.Put(context.Background(), bytes.NewReader(orphanPayload), int64(len(orphanPayload)))
	if err != nil {
		t.Fatal(err)
	}
	misplacedDirectory := filepath.Join(local.objects, "ff")
	if err := os.MkdirAll(misplacedDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	misplacedPath := filepath.Join(misplacedDirectory, fmt.Sprintf("%x", corruptDigest[:]))
	if err := os.WriteFile(misplacedPath, corruptPayload, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(local.staging, "abandoned"), []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}

	references := map[[sha256.Size]byte]int64{
		missingDigest: 7,
		corruptDigest: corruptSize,
	}
	states, quarantined, deleted, err := local.Reconcile(context.Background(), references, now, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if states[missingDigest] != string(IntegrityMissing) || states[corruptDigest] != string(IntegrityCorrupt) {
		t.Fatalf("states = %v", states)
	}
	if quarantined != 3 || deleted != 0 {
		t.Fatalf("reconcile quarantined/deleted = %d/%d", quarantined, deleted)
	}
	staging, err := os.ReadDir(local.staging)
	if err != nil {
		t.Fatal(err)
	}
	if len(staging) != 0 {
		t.Fatalf("staging entries = %d", len(staging))
	}
	quarantine, err := os.ReadDir(local.quarantine)
	if err != nil {
		t.Fatal(err)
	}
	if len(quarantine) != 3 {
		t.Fatalf("quarantine entries = %d", len(quarantine))
	}
	for _, entry := range quarantine {
		info, err := entry.Info()
		if err != nil {
			t.Fatal(err)
		}
		if !info.ModTime().Equal(now) {
			t.Fatalf("quarantine time = %v, want %v", info.ModTime(), now)
		}
	}

	states, quarantined, deleted, err = local.Reconcile(context.Background(), references, now.Add(25*time.Hour), 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if states[missingDigest] != string(IntegrityMissing) || states[corruptDigest] != string(IntegrityMissing) {
		t.Fatalf("second states = %v", states)
	}
	if quarantined != 0 || deleted != 1 {
		t.Fatalf("second reconcile quarantined/deleted = %d/%d", quarantined, deleted)
	}
	quarantine, err = os.ReadDir(local.quarantine)
	if err != nil {
		t.Fatal(err)
	}
	if len(quarantine) != 2 {
		t.Fatalf("referenced quarantine was collected: entries = %d", len(quarantine))
	}
}

func TestLocalConcurrentPutPublishesOneVerifiedObject(t *testing.T) {
	local := openTestLocal(t)
	payload := bytes.Repeat([]byte("same-content"), 1000)
	wantDigest := sha256.Sum256(payload)
	const workers = 16
	var wait sync.WaitGroup
	errorsByWorker := make(chan error, workers)
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			digest, size, err := local.Put(context.Background(), bytes.NewReader(payload), int64(len(payload)))
			if err == nil && (digest != wantDigest || size != int64(len(payload))) {
				err = errors.New("unexpected digest or size")
			}
			errorsByWorker <- err
		}()
	}
	wait.Wait()
	close(errorsByWorker)
	for err := range errorsByWorker {
		if err != nil {
			t.Fatal(err)
		}
	}
	content, err := local.Open(context.Background(), wantDigest, int64(len(payload)))
	if err != nil {
		t.Fatal(err)
	}
	content.Close()
}

func TestOpenLocalRejectsRelativeAndSymlinkRoots(t *testing.T) {
	if _, err := OpenLocal("relative"); err == nil {
		t.Fatal("accepted relative root")
	}
	target := filepath.Join(t.TempDir(), "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenLocal(link); err == nil {
		t.Fatal("accepted symlink root")
	}
}

type boundedReader struct {
	reader     io.Reader
	maxRequest int
}

func (r *boundedReader) Read(buffer []byte) (int, error) {
	if len(buffer) > r.maxRequest {
		r.maxRequest = len(buffer)
	}
	return r.reader.Read(buffer)
}

type zeroReader struct{}

func (zeroReader) Read([]byte) (int, error) {
	return 0, nil
}

type zeroStream struct{}

func (zeroStream) Read(buffer []byte) (int, error) {
	clear(buffer)
	return len(buffer), nil
}

func openTestLocal(t *testing.T) *Local {
	t.Helper()
	local, err := OpenLocal(filepath.Join(t.TempDir(), "artifacts"))
	if err != nil {
		t.Fatal(err)
	}
	return local
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %o, want %o", path, got, want)
	}
}
