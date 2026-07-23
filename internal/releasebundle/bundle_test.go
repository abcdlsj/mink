package releasebundle

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestBuildOpenCopyAndRejectMutations(t *testing.T) {
	sources := t.TempDir()
	binary := filepath.Join(sources, "sumi")
	if err := os.WriteFile(binary, []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	web := filepath.Join(sources, "web")
	if err := os.Mkdir(web, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(web, "index.html"), []byte("index"), 0o600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "bundle")
	if err := Build(BuildConfig{Version: "1.2.3", OS: runtime.GOOS, Arch: runtime.GOARCH, Binary: binary, WebRoot: web, Output: output}); err != nil {
		t.Fatal(err)
	}
	bundle, err := Open(output, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	installed := filepath.Join(t.TempDir(), "installed")
	if err := bundle.CopyTo(installed); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(installed, runtime.GOOS, runtime.GOARCH); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(output, "web", "index.html"), []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(output, runtime.GOOS, runtime.GOARCH); err == nil {
		t.Fatal("tampered bundle was accepted")
	}
}

func TestBundleRejectsTraversalSymlinkUnknownPlatformAndExtraFiles(t *testing.T) {
	bundleRoot := fixtureBundle(t)
	manifestPath := filepath.Join(bundleRoot, "manifest.json")
	payload, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := json.Unmarshal(payload, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Files[0].Path = "../escape"
	writeManifest(t, manifestPath, manifest)
	if _, err := Open(bundleRoot, runtime.GOOS, runtime.GOARCH); err == nil {
		t.Fatal("traversal manifest was accepted")
	}

	bundleRoot = fixtureBundle(t)
	if err := os.Remove(filepath.Join(bundleRoot, "web", "index.html")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(bundleRoot, "bin", "sumi"), filepath.Join(bundleRoot, "web", "index.html")); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(bundleRoot, runtime.GOOS, runtime.GOARCH); err == nil {
		t.Fatal("symlink bundle was accepted")
	}

	bundleRoot = fixtureBundle(t)
	if _, err := Open(bundleRoot, "wrong", runtime.GOARCH); err == nil {
		t.Fatal("wrong platform bundle was accepted")
	}
	if err := os.WriteFile(filepath.Join(bundleRoot, "extra"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(bundleRoot, runtime.GOOS, runtime.GOARCH); err == nil {
		t.Fatal("unmanifested file was accepted")
	}
}

func fixtureBundle(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	binary := filepath.Join(root, "source")
	if err := os.WriteFile(binary, []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	web := filepath.Join(root, "source-web")
	if err := os.Mkdir(web, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(web, "index.html"), []byte("index"), 0o600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "bundle")
	if err := Build(BuildConfig{Version: "1.0.0", OS: runtime.GOOS, Arch: runtime.GOARCH, Binary: binary, WebRoot: web, Output: output}); err != nil {
		t.Fatal(err)
	}
	return output
}

func writeManifest(t *testing.T, path string, manifest Manifest) {
	t.Helper()
	payload, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
}
