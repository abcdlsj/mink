package pairing

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/abcdlsj/sumi/internal/endpoint"
)

func TestBundleStrictRoundTripAndNoClobber(t *testing.T) {
	now := time.Now().UTC()
	server, err := endpoint.Parse("http://127.0.0.1:8080", "")
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := New(server, now.Add(10*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "pair.json")
	if err := WriteNew(path, bundle); err != nil {
		t.Fatal(err)
	}
	opened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if opened.Bundle != bundle {
		t.Fatalf("bundle = %+v, want %+v", opened.Bundle, bundle)
	}
	if err := opened.Bundle.ValidateAt(now); err != nil {
		t.Fatal(err)
	}
	opened.Close()
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("bundle mode = %v, %v", info.Mode(), err)
	}
	if err := WriteNew(path, bundle); err == nil {
		t.Fatal("existing bundle was overwritten")
	}
	payload, err := os.ReadFile(path)
	if err != nil || len(payload) == 0 {
		t.Fatalf("bundle after no-clobber = %q, %v", payload, err)
	}
}

func TestBundleRejectsUnknownInvalidAndSymlink(t *testing.T) {
	directory := t.TempDir()
	for name, payload := range map[string]string{
		"unknown":  `{"version":1,"unknown":true}`,
		"version":  `{"version":2}`,
		"trailing": `{"version":1} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(directory, name)
			if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Open(path); err == nil {
				t.Fatal("invalid bundle was accepted")
			}
		})
	}
	target := filepath.Join(directory, "target")
	if err := os.WriteFile(target, []byte("secret sentinel"), 0o600); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(directory, "link")
	if err := os.Symlink(target, symlink); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(symlink); err == nil {
		t.Fatal("symlink bundle was accepted")
	}
}

func TestDiscardIsExpiryOnlyAndChecksOpenedIdentity(t *testing.T) {
	now := time.Now().UTC()
	server, err := endpoint.Parse("https://example.com", "sha256/"+base64.RawURLEncoding.EncodeToString(make([]byte, sha256.Size)))
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := New(server, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "pair.json")
	if err := WriteNew(path, bundle); err != nil {
		t.Fatal(err)
	}
	opened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := opened.Discard(now); !errors.Is(err, ErrStillValid) {
		t.Fatalf("early discard = %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("early discard removed bundle: %v", err)
	}
	if err := opened.Discard(bundle.ExpiresAt); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired bundle remains: %v", err)
	}

	if err := WriteNew(path, bundle); err != nil {
		t.Fatal(err)
	}
	opened, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(filepath.Dir(path), "target")
	if err := os.WriteFile(target, []byte("sentinel"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if err := opened.Discard(bundle.ExpiresAt); err == nil {
		t.Fatal("replacement symlink was removed")
	}
	payload, err := os.ReadFile(target)
	if err != nil || string(payload) != "sentinel" {
		t.Fatalf("replacement target = %q, %v", payload, err)
	}
	opened.Close()
}
