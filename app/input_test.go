package app

import (
	"testing"

	"github.com/abcdlsj/sumi/config"
)

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

func TestInputFlowMemoryScopesPreferConversationBeforePersona(t *testing.T) {
	a, err := New(config.Config{
		Runtime:   "stub",
		DataDir:   t.TempDir(),
		Workspace: "/workspace",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })

	scopes := inputFlow{app: a, personaID: "andy"}.memoryScopes("desktop:agent:space-1", "desktop:agent:space-1")
	if len(scopes) < 4 {
		t.Fatalf("scopes = %#v, want channel/persona/workspace/global", scopes)
	}
	if scopes[0].Kind != "channel" || scopes[0].Key != "desktop:agent:space-1" {
		t.Fatalf("first scope = %#v, want current channel", scopes[0])
	}
	if scopes[1].Kind != "persona" || scopes[1].Key != "andy" {
		t.Fatalf("second scope = %#v, want persona", scopes[1])
	}
	if scopes[2].Kind != "workspace" || scopes[3].Kind != "global" {
		t.Fatalf("tail scopes = %#v", scopes)
	}
}
