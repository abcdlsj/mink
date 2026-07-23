package userdirs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveUserDirectories(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	tests := []struct {
		name        string
		goos        string
		environment map[string]string
		wantState   string
		wantRuntime string
	}{
		{"darwin", "darwin", nil, filepath.Join(home, "Library", "Application Support", "Sumi"), filepath.Join(home, "Library", "Caches", "Sumi", "runtime")},
		{"linux defaults", "linux", nil, filepath.Join(home, ".local", "state", "sumi"), filepath.Join(home, ".local", "state", "sumi", "runtime")},
		{"linux xdg", "linux", map[string]string{"XDG_STATE_HOME": filepath.Join(home, "state"), "XDG_RUNTIME_DIR": filepath.Join(home, "run")}, filepath.Join(home, "state", "sumi"), filepath.Join(home, "run", "sumi")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			layout, err := resolve(home, test.goos, func(key string) string { return test.environment[key] })
			if err != nil {
				t.Fatal(err)
			}
			if layout.StateRoot != test.wantState || layout.Runtime != test.wantRuntime || layout.HumanCredential != filepath.Join(test.wantState, "credentials", "human.key") {
				t.Fatalf("layout = %+v", layout)
			}
		})
	}
	if _, err := resolve(home, "linux", func(string) string { return "relative" }); err == nil {
		t.Fatal("relative XDG path was accepted")
	}
}

func TestEnsureUserDirectoriesRejectsSymlinkAndLooseMode(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	if err := ensurePrivateDirectory(root); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(root)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("state mode = %v, %v", info.Mode(), err)
	}
	loose := filepath.Join(t.TempDir(), "loose")
	if err := os.Mkdir(loose, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ensurePrivateDirectory(loose); err == nil {
		t.Fatal("loose directory was accepted")
	}
	target := filepath.Join(t.TempDir(), "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(target, symlink); err != nil {
		t.Fatal(err)
	}
	if err := ensurePrivateDirectory(symlink); err == nil {
		t.Fatal("symlink directory was accepted")
	}
}
