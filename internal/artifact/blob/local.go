package blob

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

const (
	MaxBlobSize  int64 = 64 << 20
	MaxChunkSize       = 64 << 10
)

var (
	ErrBlobTooLarge  = errors.New("artifact blob exceeds size limit")
	ErrBlobMissing   = errors.New("artifact blob is missing")
	ErrBlobIntegrity = errors.New("artifact blob integrity failure")
	ErrDigestInvalid = errors.New("artifact digest is invalid")
	digestPattern    = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type IntegrityState string

const (
	IntegrityReady   IntegrityState = "ready"
	IntegrityMissing IntegrityState = "missing"
	IntegrityCorrupt IntegrityState = "corrupt"
)

type Local struct {
	root       string
	objects    string
	staging    string
	quarantine string
}

func OpenLocal(root string) (*Local, error) {
	if root == "" || !filepath.IsAbs(root) {
		return nil, fmt.Errorf("artifact blob root must be absolute")
	}
	local := &Local{
		root:       root,
		objects:    filepath.Join(root, "objects"),
		staging:    filepath.Join(root, "staging"),
		quarantine: filepath.Join(root, "quarantine"),
	}
	for _, path := range []string{local.root, local.objects, local.staging, local.quarantine} {
		if err := secureDirectory(path); err != nil {
			return nil, err
		}
	}
	if same, err := sameFilesystem(local.staging, local.objects); err != nil {
		return nil, err
	} else if !same {
		return nil, fmt.Errorf("artifact staging and objects must share a filesystem")
	}
	return local, nil
}

func (l *Local) Put(ctx context.Context, source io.Reader, limit int64) ([sha256.Size]byte, int64, error) {
	if source == nil || limit <= 0 || limit > MaxBlobSize {
		return [sha256.Size]byte{}, 0, fmt.Errorf("invalid artifact blob stream limit")
	}
	temporary, err := os.CreateTemp(l.staging, "put-*")
	if err != nil {
		return [sha256.Size]byte{}, 0, fmt.Errorf("create artifact staging file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return [sha256.Size]byte{}, 0, fmt.Errorf("secure artifact staging file: %w", err)
	}
	temporaryInfo, err := temporary.Stat()
	if err != nil {
		temporary.Close()
		return [sha256.Size]byte{}, 0, fmt.Errorf("inspect artifact staging file: %w", err)
	}
	if !temporaryInfo.Mode().IsRegular() || temporaryInfo.Mode().Perm() != 0o600 || !hasSingleLink(temporaryInfo) {
		temporary.Close()
		return [sha256.Size]byte{}, 0, ErrBlobIntegrity
	}

	hash := sha256.New()
	buffer := make([]byte, MaxChunkSize)
	var size int64
	for {
		if err := ctx.Err(); err != nil {
			temporary.Close()
			return [sha256.Size]byte{}, 0, err
		}
		read, readErr := source.Read(buffer)
		if read > 0 {
			if size+int64(read) > limit {
				temporary.Close()
				return [sha256.Size]byte{}, 0, ErrBlobTooLarge
			}
			chunk := buffer[:read]
			if _, err := hash.Write(chunk); err != nil {
				temporary.Close()
				return [sha256.Size]byte{}, 0, fmt.Errorf("hash artifact blob: %w", err)
			}
			if _, err := temporary.Write(chunk); err != nil {
				temporary.Close()
				return [sha256.Size]byte{}, 0, fmt.Errorf("write artifact staging file: %w", err)
			}
			size += int64(read)
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				temporary.Close()
				return [sha256.Size]byte{}, 0, fmt.Errorf("read artifact blob: %w", readErr)
			}
			break
		}
		if read == 0 {
			temporary.Close()
			return [sha256.Size]byte{}, 0, fmt.Errorf("artifact blob reader made no progress")
		}
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return [sha256.Size]byte{}, 0, fmt.Errorf("sync artifact staging file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return [sha256.Size]byte{}, 0, fmt.Errorf("close artifact staging file: %w", err)
	}

	var digest [sha256.Size]byte
	copy(digest[:], hash.Sum(nil))
	directory, path := l.objectPath(digest)
	if err := secureDirectory(directory); err != nil {
		return [sha256.Size]byte{}, 0, err
	}
	if err := syncDirectory(l.objects); err != nil {
		return [sha256.Size]byte{}, 0, err
	}
	if err := verifyPublishedFile(ctx, path, digest, size); err == nil {
		return digest, size, nil
	} else if !errors.Is(err, ErrBlobMissing) {
		return [sha256.Size]byte{}, 0, err
	}
	if err := os.Link(temporaryPath, path); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return [sha256.Size]byte{}, 0, fmt.Errorf("publish artifact blob: %w", err)
		}
		verifyErr := verifyPublishedFile(ctx, path, digest, size)
		if verifyErr == nil {
			return digest, size, nil
		}
		return [sha256.Size]byte{}, 0, verifyErr
	}
	if err := os.Remove(temporaryPath); err != nil {
		return [sha256.Size]byte{}, 0, fmt.Errorf("remove published artifact staging file: %w", err)
	}
	if err := syncDirectory(l.staging); err != nil {
		return [sha256.Size]byte{}, 0, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return [sha256.Size]byte{}, 0, fmt.Errorf("secure artifact blob: %w", err)
	}
	publishedInfo, err := os.Lstat(path)
	if err != nil {
		return [sha256.Size]byte{}, 0, fmt.Errorf("inspect published artifact blob: %w", err)
	}
	if !publishedInfo.Mode().IsRegular() || publishedInfo.Mode().Perm() != 0o600 || !hasSingleLink(publishedInfo) {
		return [sha256.Size]byte{}, 0, ErrBlobIntegrity
	}
	if err := syncDirectory(directory); err != nil {
		return [sha256.Size]byte{}, 0, err
	}
	return digest, size, nil
}

func (l *Local) Open(ctx context.Context, digest [sha256.Size]byte, size int64) (io.ReadCloser, error) {
	_, path := l.objectPath(digest)
	file, err := verifiedFile(ctx, path, digest, size)
	if err != nil {
		return nil, err
	}
	return file, nil
}

func (l *Local) Reconcile(ctx context.Context, references map[[sha256.Size]byte]int64, now time.Time, retention time.Duration) (map[[sha256.Size]byte]string, int, int, error) {
	if now.IsZero() || retention < 0 {
		return nil, 0, 0, fmt.Errorf("invalid artifact reconciliation time")
	}
	states := make(map[[sha256.Size]byte]string, len(references))
	quarantined := 0
	if err := l.clearStaging(); err != nil {
		return nil, 0, 0, err
	}
	for digest, size := range references {
		if err := ctx.Err(); err != nil {
			return nil, 0, 0, err
		}
		_, path := l.objectPath(digest)
		err := verifyFile(path, digest, size)
		switch {
		case err == nil:
			states[digest] = string(IntegrityReady)
		case errors.Is(err, ErrBlobMissing):
			states[digest] = string(IntegrityMissing)
		default:
			states[digest] = string(IntegrityCorrupt)
			if moveErr := l.moveToQuarantine(path, digest, "corrupt", now); moveErr == nil {
				quarantined++
			}
		}
	}
	if err := l.quarantineOrphans(references, now, &quarantined); err != nil {
		return nil, 0, 0, err
	}
	deleted, err := l.collectQuarantine(references, now, retention)
	if err != nil {
		return nil, 0, 0, err
	}
	return states, quarantined, deleted, nil
}

func (l *Local) objectPath(digest [sha256.Size]byte) (string, string) {
	encoded := hex.EncodeToString(digest[:])
	directory := filepath.Join(l.objects, encoded[:2])
	return directory, filepath.Join(directory, encoded)
}

func (l *Local) clearStaging() error {
	entries, err := os.ReadDir(l.staging)
	if err != nil {
		return fmt.Errorf("read artifact staging directory: %w", err)
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(l.staging, entry.Name())); err != nil {
			return fmt.Errorf("clear artifact staging entry: %w", err)
		}
	}
	return syncDirectory(l.staging)
}

func (l *Local) quarantineOrphans(references map[[sha256.Size]byte]int64, now time.Time, quarantined *int) error {
	return filepath.WalkDir(l.objects, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == l.objects || entry.IsDir() {
			return nil
		}
		name := entry.Name()
		decoded, err := hex.DecodeString(name)
		var digest [sha256.Size]byte
		if err == nil && len(decoded) == sha256.Size && digestPattern.MatchString(name) {
			copy(digest[:], decoded)
			if _, referenced := references[digest]; referenced {
				_, expectedPath := l.objectPath(digest)
				if filepath.Clean(path) == expectedPath {
					return nil
				}
			}
		}
		if err := l.moveToQuarantine(path, digest, "orphan", now); err == nil {
			*quarantined = *quarantined + 1
		}
		return nil
	})
}

func (l *Local) moveToQuarantine(path string, digest [sha256.Size]byte, reason string, now time.Time) error {
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect artifact before quarantine: %w", err)
	}
	encoded := hex.EncodeToString(digest[:])
	if strings.Trim(encoded, "0") == "" {
		encoded = "unknown"
	}
	target := filepath.Join(l.quarantine, fmt.Sprintf("%s.%d.%s", encoded, now.UnixNano(), reason))
	if err := os.Rename(path, target); err != nil {
		return fmt.Errorf("quarantine artifact blob: %w", err)
	}
	if err := os.Chtimes(target, now, now); err != nil {
		return fmt.Errorf("record artifact quarantine time: %w", err)
	}
	return syncDirectory(l.quarantine)
}

func (l *Local) collectQuarantine(references map[[sha256.Size]byte]int64, now time.Time, retention time.Duration) (int, error) {
	entries, err := os.ReadDir(l.quarantine)
	if err != nil {
		return 0, fmt.Errorf("read artifact quarantine: %w", err)
	}
	deleted := 0
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return 0, fmt.Errorf("inspect artifact quarantine entry: %w", err)
		}
		if now.Sub(info.ModTime()) < retention {
			continue
		}
		name := strings.SplitN(entry.Name(), ".", 2)[0]
		decoded, decodeErr := hex.DecodeString(name)
		if decodeErr == nil && len(decoded) == sha256.Size {
			var digest [sha256.Size]byte
			copy(digest[:], decoded)
			if _, referenced := references[digest]; referenced {
				continue
			}
		}
		if err := os.RemoveAll(filepath.Join(l.quarantine, entry.Name())); err != nil {
			return 0, fmt.Errorf("collect artifact quarantine entry: %w", err)
		}
		deleted++
	}
	if deleted > 0 {
		if err := syncDirectory(l.quarantine); err != nil {
			return 0, err
		}
	}
	return deleted, nil
}

func secureDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("create artifact directory: %w", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect artifact directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("artifact path is not a regular directory")
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return fmt.Errorf("secure artifact directory: %w", err)
	}
	return nil
}

func verifiedFile(ctx context.Context, path string, digest [sha256.Size]byte, size int64) (*os.File, error) {
	linkInfo, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrBlobMissing
	}
	if err != nil {
		return nil, fmt.Errorf("inspect artifact blob: %w", err)
	}
	if linkInfo.Mode()&os.ModeSymlink != 0 || !linkInfo.Mode().IsRegular() || linkInfo.Mode().Perm() != 0o600 || !hasSingleLink(linkInfo) {
		return nil, ErrBlobIntegrity
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open artifact blob: %w", err)
	}
	fileInfo, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("inspect open artifact blob: %w", err)
	}
	if !os.SameFile(linkInfo, fileInfo) || !fileInfo.Mode().IsRegular() || !hasSingleLink(fileInfo) || fileInfo.Size() != size {
		file.Close()
		return nil, ErrBlobIntegrity
	}
	hash := sha256.New()
	buffer := make([]byte, MaxChunkSize)
	for {
		if err := ctx.Err(); err != nil {
			file.Close()
			return nil, err
		}
		read, readErr := file.Read(buffer)
		if read > 0 {
			_, _ = hash.Write(buffer[:read])
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				file.Close()
				return nil, fmt.Errorf("verify artifact blob: %w", readErr)
			}
			break
		}
	}
	if !equalDigest(hash.Sum(nil), digest) {
		file.Close()
		return nil, ErrBlobIntegrity
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		file.Close()
		return nil, fmt.Errorf("rewind artifact blob: %w", err)
	}
	return file, nil
}

func verifyFile(path string, digest [sha256.Size]byte, size int64) error {
	file, err := verifiedFile(context.Background(), path, digest, size)
	if err != nil {
		return err
	}
	return file.Close()
}

func equalDigest(value []byte, digest [sha256.Size]byte) bool {
	if len(value) != len(digest) {
		return false
	}
	var difference byte
	for index := range digest {
		difference |= value[index] ^ digest[index]
	}
	return difference == 0
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open artifact directory for sync: %w", err)
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync artifact directory: %w", err)
	}
	return nil
}

func sameFilesystem(first, second string) (bool, error) {
	firstInfo, err := os.Stat(first)
	if err != nil {
		return false, fmt.Errorf("inspect artifact filesystem: %w", err)
	}
	secondInfo, err := os.Stat(second)
	if err != nil {
		return false, fmt.Errorf("inspect artifact filesystem: %w", err)
	}
	firstStat, firstOK := firstInfo.Sys().(*syscall.Stat_t)
	secondStat, secondOK := secondInfo.Sys().(*syscall.Stat_t)
	if !firstOK || !secondOK {
		return false, fmt.Errorf("artifact filesystem identity is unavailable")
	}
	return uint64(firstStat.Dev) == uint64(secondStat.Dev), nil
}

func hasSingleLink(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && uint64(stat.Nlink) == 1
}

func verifyPublishedFile(ctx context.Context, path string, digest [sha256.Size]byte, size int64) error {
	timeout := time.NewTimer(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer timeout.Stop()
	defer ticker.Stop()
	for {
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			return ErrBlobMissing
		}
		if err != nil {
			return fmt.Errorf("inspect concurrently published artifact blob: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			return ErrBlobIntegrity
		}
		if hasSingleLink(info) {
			return verifyFile(path, digest, size)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout.C:
			return ErrBlobIntegrity
		case <-ticker.C:
		}
	}
}
