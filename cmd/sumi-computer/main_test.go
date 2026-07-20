package main

import (
	"testing"

	computerv1 "github.com/abcdlsj/sumi/gen/go/sumi/computer/v1"
)

func TestPlatform(t *testing.T) {
	tests := []struct {
		goos   string
		goarch string
		os     computerv1.OperatingSystem
		arch   computerv1.Architecture
	}{
		{"darwin", "arm64", computerv1.OperatingSystem_OPERATING_SYSTEM_MACOS, computerv1.Architecture_ARCHITECTURE_ARM64},
		{"linux", "amd64", computerv1.OperatingSystem_OPERATING_SYSTEM_LINUX, computerv1.Architecture_ARCHITECTURE_AMD64},
	}
	for _, test := range tests {
		osName, arch, err := platform(test.goos, test.goarch)
		if err != nil {
			t.Fatal(err)
		}
		if osName != test.os || arch != test.arch {
			t.Fatalf("platform(%q, %q) = %v, %v", test.goos, test.goarch, osName, arch)
		}
	}
	if _, _, err := platform("windows", "amd64"); err == nil {
		t.Fatal("windows error = nil")
	}
	if _, _, err := platform("linux", "386"); err == nil {
		t.Fatal("386 error = nil")
	}
}
