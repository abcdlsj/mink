package app

import "testing"

func TestRuntimeForPermissionUsesNativeForRestrictedExternalRuntimes(t *testing.T) {
	for _, runtime := range []string{"claude", "codex"} {
		if got := runtimeForPermission(runtime, "telegram"); got != "native" {
			t.Fatalf("telegram %s runtime = %q, want native", runtime, got)
		}
		if got := runtimeForPermission(runtime, "cron"); got != "native" {
			t.Fatalf("cron %s runtime = %q, want native", runtime, got)
		}
	}
}

func TestRuntimeForPermissionLeavesDefaultContextAlone(t *testing.T) {
	if got := runtimeForPermission("codex", "default"); got != "codex" {
		t.Fatalf("default runtime = %q, want codex", got)
	}
	if got := runtimeForPermission("stub", "cron"); got != "stub" {
		t.Fatalf("non-external restricted runtime = %q, want stub", got)
	}
}
