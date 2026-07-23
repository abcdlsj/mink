package configfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigRoundTripAndStrictSchema(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "home")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "config.toml")
	if err := Ensure(path); err != nil {
		t.Fatal(err)
	}
	config := Default()
	config.Server = ServerConfig{Origin: "https://example.com", Identity: "spki-pin", SPKIPin: "sha256/test"}
	if err := Save(path, config); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil || loaded != config {
		t.Fatalf("Load() = %+v, %v", loaded, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode = %v, %v", info.Mode(), err)
	}

	if err := os.WriteFile(path, []byte("version = 1\nunknown = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("unknown config field was accepted")
	}
	if err := os.WriteFile(path, []byte("version = 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("unknown config version was accepted")
	}
}

func TestConfigRejectsUnsafePathsAndKeepsExistingOnFailure(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "home")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(directory, "target")
	if err := os.WriteFile(target, []byte("version = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(directory, "config.toml")
	if err := os.Symlink(target, symlink); err != nil {
		t.Fatal(err)
	}
	if err := Save(symlink, Default()); err == nil {
		t.Fatal("symlink config was accepted")
	}
	payload, err := os.ReadFile(target)
	if err != nil || string(payload) != "version = 1\n" {
		t.Fatalf("target changed = %q, %v", payload, err)
	}
	if err := os.Remove(symlink); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(symlink, []byte("version = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(symlink); err == nil {
		t.Fatal("loose config mode was accepted")
	}
}
