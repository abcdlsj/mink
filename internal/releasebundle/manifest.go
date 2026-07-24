package releasebundle

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/sys/unix"
)

const ManifestVersion = 1

var releaseVersionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

type File struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type Manifest struct {
	Version        int    `json:"version"`
	ReleaseVersion string `json:"release_version"`
	OS             string `json:"os"`
	Arch           string `json:"arch"`
	Files          []File `json:"files"`
}

type Bundle struct {
	Root     string
	Manifest Manifest
}

func ValidateManifest(manifest Manifest) error {
	_, err := decodeManifest(mustJSON(manifest))
	return err
}

func Open(root, expectedOS, expectedArch string) (Bundle, error) {
	info, err := os.Lstat(root)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return Bundle{}, errors.New("release bundle root is unsafe")
	}
	payload, err := readNoFollow(filepath.Join(root, "manifest.json"), 1<<20)
	if err != nil {
		return Bundle{}, errors.New("release bundle manifest is missing or unsafe")
	}
	manifest, err := decodeManifest(payload)
	if err != nil {
		return Bundle{}, err
	}
	if manifest.OS != expectedOS || manifest.Arch != expectedArch {
		return Bundle{}, errors.New("release bundle platform does not match this computer")
	}
	bundle := Bundle{Root: root, Manifest: manifest}
	if err := bundle.Verify(); err != nil {
		return Bundle{}, err
	}
	return bundle, nil
}

func (b Bundle) Verify() error {
	want := map[string]File{}
	for _, f := range b.Manifest.Files {
		want[f.Path] = f
		p := filepath.Join(b.Root, filepath.FromSlash(f.Path))
		info, err := os.Lstat(p)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() != f.Size {
			return errors.New("release bundle file is missing or unsafe")
		}
		if f.Path == "bin/sumi" && info.Mode().Perm()&0o111 == 0 {
			return errors.New("release bundle executable is not executable")
		}
		digest, err := hashNoFollow(p)
		if err != nil || !strings.EqualFold(digest, f.SHA256) {
			return errors.New("release bundle file hash does not match manifest")
		}
	}
	seen := map[string]bool{"manifest.json": true}
	err := filepath.WalkDir(b.Root, func(p string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if p == b.Root {
			return nil
		}
		rel, err := filepath.Rel(b.Root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("release bundle contains a symlink")
		}
		if entry.IsDir() {
			return nil
		}
		if rel == "manifest.json" {
			return nil
		}
		if _, ok := want[rel]; !ok {
			return errors.New("release bundle contains an unmanifested file")
		}
		seen[rel] = true
		return nil
	})
	if err != nil {
		return err
	}
	if len(seen) != len(want)+1 {
		return errors.New("release bundle is incomplete")
	}
	return nil
}

func (b Bundle) CopyTo(dst string) error {
	return b.copyTo(dst, nil)
}

func (b Bundle) copyTo(dst string, beforeCopy func(string) error) error {
	if err := b.Verify(); err != nil {
		return err
	}
	if err := os.Mkdir(dst, 0o700); err != nil {
		if errors.Is(err, os.ErrExist) {
			return errors.New("installed release version already exists")
		}
		return errors.New("create installed release version")
	}
	cleanup := true
	defer func() {
		if cleanup {
			os.RemoveAll(dst)
		}
	}()
	for _, f := range b.Manifest.Files {
		if beforeCopy != nil {
			if err := beforeCopy(f.Path); err != nil {
				return err
			}
		}
		src := filepath.Join(b.Root, filepath.FromSlash(f.Path))
		targ := filepath.Join(dst, filepath.FromSlash(f.Path))
		if err := copyRegular(src, targ, f.Path == "bin/sumi"); err != nil {
			return err
		}
	}
	payload, err := json.MarshalIndent(b.Manifest, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	if err := writeNew(filepath.Join(dst, "manifest.json"), payload, 0o600); err != nil {
		return err
	}
	if err := syncDirectory(dst); err != nil {
		return err
	}
	if _, err := Open(dst, b.Manifest.OS, b.Manifest.Arch); err != nil {
		return errors.New("installed release copy does not match manifest")
	}
	cleanup = false
	return nil
}

func decodeManifest(payload []byte) (Manifest, error) {
	var manifest Manifest
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, errors.New("release bundle manifest is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Manifest{}, errors.New("release bundle manifest is invalid")
	}
	if manifest.Version != ManifestVersion || !releaseVersionPattern.MatchString(manifest.ReleaseVersion) || manifest.OS == "" || manifest.Arch == "" {
		return Manifest{}, errors.New("release bundle manifest is invalid")
	}
	paths := map[string]bool{}
	hasBinary := false
	hasWeb := false
	for _, file := range manifest.Files {
		if !validBundlePath(file.Path) || file.Size < 0 || len(file.SHA256) != sha256.Size*2 {
			return Manifest{}, errors.New("release bundle manifest is invalid")
		}
		if _, err := hex.DecodeString(file.SHA256); err != nil || paths[file.Path] {
			return Manifest{}, errors.New("release bundle manifest is invalid")
		}
		paths[file.Path] = true
		hasBinary = hasBinary || file.Path == "bin/sumi"
		hasWeb = hasWeb || strings.HasPrefix(file.Path, "web/")
	}
	if !hasBinary || !hasWeb {
		return Manifest{}, errors.New("release bundle must contain bin/sumi and web assets")
	}
	return manifest, nil
}

func validBundlePath(path string) bool {
	if path == "" || strings.Contains(path, "\\") || strings.ContainsRune(path, 0) || filepath.IsAbs(path) || filepath.Clean(path) != filepath.FromSlash(path) {
		return false
	}
	return path == "bin/sumi" || strings.HasPrefix(path, "web/")
}

func readNoFollow(path string, limit int64) ([]byte, error) {
	descriptor, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(descriptor), path)
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("file is not regular")
	}
	payload, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(payload)) > limit {
		return nil, errors.New("file is too large")
	}
	return payload, nil
}

func hashNoFollow(path string) (string, error) {
	descriptor, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return "", err
	}
	file := os.NewFile(uintptr(descriptor), path)
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func copyRegular(source, target string, executable bool) error {
	payload, err := readNoFollow(source, 1<<31)
	if err != nil {
		return errors.New("read release bundle file")
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return errors.New("create release bundle directory")
	}
	mode := os.FileMode(0o600)
	if executable {
		mode = 0o700
	}
	return writeNew(target, payload, mode)
}

func writeNew(path string, payload []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	failed := true
	defer func() {
		file.Close()
		if failed {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(payload); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	failed = false
	return nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func sortedFiles(files []File) []File {
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files
}

func fileRecord(path, relative string) (File, error) {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return File{}, fmt.Errorf("bundle source file %q is unsafe", relative)
	}
	digest, err := hashNoFollow(path)
	if err != nil {
		return File{}, err
	}
	return File{Path: relative, SHA256: digest, Size: info.Size()}, nil
}
