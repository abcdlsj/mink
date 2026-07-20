package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/abcdlsj/sumi/internal/placementcode"
	"github.com/google/uuid"
)

func TestProvisionCreatesCanonicalPrivateWorkspaceAndPreservesContent(t *testing.T) {
	root := filepath.Join(t.TempDir(), "computer")
	agentID := uuid.NewString()
	workspace, err := Provision(root, "{"+agentID+"}")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "agents", "agent_"+agentID, "workspace")
	if workspace != want {
		t.Fatalf("workspace = %q, want %q", workspace, want)
	}
	marker := filepath.Join(workspace, "keep.txt")
	if err := os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{root, filepath.Join(root, "agents"), filepath.Dir(workspace), workspace} {
		if err := os.Chmod(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	second, err := Provision(root, agentID)
	if err != nil {
		t.Fatal(err)
	}
	if second != workspace {
		t.Fatalf("reused workspace = %q, want %q", second, workspace)
	}
	content, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "keep" {
		t.Fatalf("marker content = %q", content)
	}
	for _, path := range []string{root, filepath.Join(root, "agents"), filepath.Dir(workspace), workspace} {
		assertPrivateDirectory(t, path)
	}
}

func TestProvisionRejectsNonDirectoryAndSymlinkComponents(t *testing.T) {
	agentID := uuid.NewString()
	tests := []struct {
		name    string
		code    string
		prepare func(*testing.T, string)
	}{
		{
			name: "root file",
			code: placementcode.WorkspaceRootInvalid,
			prepare: func(t *testing.T, root string) {
				if err := os.WriteFile(root, []byte("file"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "root symlink",
			code: placementcode.WorkspaceRootInvalid,
			prepare: func(t *testing.T, root string) {
				target := filepath.Join(t.TempDir(), "target")
				if err := os.Mkdir(target, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, root); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "agents file",
			code: placementcode.WorkspaceRootInvalid,
			prepare: func(t *testing.T, root string) {
				if err := os.Mkdir(root, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "agents"), []byte("file"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "agent home symlink",
			code: placementcode.AgentHomeInvalid,
			prepare: func(t *testing.T, root string) {
				agents := filepath.Join(root, "agents")
				if err := os.MkdirAll(agents, 0o700); err != nil {
					t.Fatal(err)
				}
				target := filepath.Join(t.TempDir(), "target")
				if err := os.Mkdir(target, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, filepath.Join(agents, "agent_"+agentID)); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "workspace file",
			code: placementcode.WorkspaceInvalid,
			prepare: func(t *testing.T, root string) {
				home := filepath.Join(root, "agents", "agent_"+agentID)
				if err := os.MkdirAll(home, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(home, "workspace"), []byte("file"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "workspace symlink",
			code: placementcode.WorkspaceInvalid,
			prepare: func(t *testing.T, root string) {
				home := filepath.Join(root, "agents", "agent_"+agentID)
				if err := os.MkdirAll(home, 0o700); err != nil {
					t.Fatal(err)
				}
				target := filepath.Join(t.TempDir(), "target")
				if err := os.Mkdir(target, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, filepath.Join(home, "workspace")); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "computer")
			test.prepare(t, root)
			_, err := Provision(root, agentID)
			var provisionError *ProvisionError
			if !errors.As(err, &provisionError) {
				t.Fatalf("error = %v, want ProvisionError", err)
			}
			if provisionError.Code != test.code {
				t.Fatalf("error code = %q, want %q", provisionError.Code, test.code)
			}
		})
	}
}

func assertPrivateDirectory(t *testing.T, path string) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		t.Fatalf("%s is not a real directory", path)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("%s mode = %o, want 700", path, got)
	}
}
