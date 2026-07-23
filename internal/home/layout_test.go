package home

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestEnsureCreatesMinimalLayout(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sumi")
	layout, err := Ensure(root)
	if err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	want := []string{"agents", "cache", "config.toml", "data", "logs"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("root entries = %v, want %v", names, want)
	}

	for _, path := range []string{layout.Root, layout.Data, layout.Artifacts, layout.Agents, layout.Cache, layout.Logs} {
		assertMode(t, path, 0o700)
	}
	assertMode(t, layout.Config, 0o600)
}

func TestEnsureRejectsFileRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sumi")
	if err := os.WriteFile(root, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Ensure(root); err == nil {
		t.Fatal("expected file root to fail")
	}
}

func TestEnsureRejectsSymlinkAndLooseEntries(t *testing.T) {
	for name, prepare := range map[string]func(string) error{
		"root symlink": func(root string) error {
			target := root + "-target"
			if err := os.Mkdir(target, 0o700); err != nil {
				return err
			}
			return os.Symlink(target, root)
		},
		"entry symlink": func(root string) error {
			if err := os.Mkdir(root, 0o700); err != nil {
				return err
			}
			return os.Symlink(t.TempDir(), filepath.Join(root, "data"))
		},
	} {
		t.Run(name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "sumi")
			if err := prepare(root); err != nil {
				t.Fatal(err)
			}
			if _, err := Ensure(root); err == nil {
				t.Fatal("unsafe layout was accepted")
			}
		})
	}
}

func TestEnsureSecuresExistingDirectoryModes(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sumi")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	layout, err := Ensure(root)
	if err != nil {
		t.Fatal(err)
	}
	assertMode(t, layout.Root, 0o700)
	assertMode(t, layout.Data, 0o700)
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %o, want %o", path, got, want)
	}
}
