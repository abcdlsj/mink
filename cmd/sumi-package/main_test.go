package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/abcdlsj/sumi/internal/releasebundle"
)

func TestRunBuildsVerifiedBundleWithQuietOutput(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "sumi-private")
	if err := os.WriteFile(binary, []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	web := filepath.Join(root, "private-web")
	if err := os.Mkdir(web, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(web, "index.html"), []byte("web"), 0o600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "private-bundle")
	var stdout bytes.Buffer
	if err := run([]string{"--version", "1.0.0", "--binary", binary, "--web", web, "--out", output}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if _, err := releasebundle.Open(output, runtime.GOOS, runtime.GOARCH); err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{binary, web, output} {
		if strings.Contains(stdout.String(), private) {
			t.Fatalf("package output leaked path: %q", stdout.String())
		}
	}
}
