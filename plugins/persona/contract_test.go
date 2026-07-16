package persona

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abcdlsj/sumi/app"
	"github.com/abcdlsj/sumi/command"
	"github.com/abcdlsj/sumi/config"
)

// These tests LOCK the persona plugin's tool surface and the invoke_persona
// argument guard. They pin current behavior only: the three persona tools stay
// distinct capabilities (Rule #2), and invoke_persona rejects empty id/input
// before dispatching a turn. The turn-dispatch path itself (HandleInputAs) is
// already covered in app/app_test.go and is intentionally not re-exercised here.

func newPersonaTestApp(t *testing.T) *app.App {
	t.Helper()
	dir := t.TempDir()
	a, err := app.New(config.Config{
		Runtime:   "stub",
		DataDir:   filepath.Join(dir, "sumi-data"),
		Workspace: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func TestPersonaToolSurfaceRegistersAllEntryPoints(t *testing.T) {
	a := newPersonaTestApp(t)
	if err := Plugin()(a); err != nil {
		t.Fatalf("apply persona plugin: %v", err)
	}
	want := []string{"list_personas", "create_persona", "invoke_persona"}
	got := map[string]bool{}
	for _, tl := range a.Tools() {
		got[tl.Name()] = true
	}
	for _, name := range want {
		if !got[name] {
			t.Errorf("persona tool %q is not registered", name)
		}
	}
}

func TestInvokePersonaRejectsEmptyArgs(t *testing.T) {
	a := newPersonaTestApp(t)
	tl := invokeTool{a: a}
	ctx := command.WithSource(context.Background(), "cli")
	cases := []map[string]string{
		{"input": "hi"},    // missing id
		{"id": "reviewer"}, // missing input
		{},                 // both missing
	}
	for _, args := range cases {
		b, _ := json.Marshal(args)
		_, err := tl.Run(ctx, b)
		if err == nil || !strings.Contains(err.Error(), "id and input are required") {
			t.Fatalf("invoke_persona args=%v err = %v, want arg-validation error", args, err)
		}
	}
}

func TestCreatePersonaRejectsEmptyID(t *testing.T) {
	a := newPersonaTestApp(t)
	tl := createTool{a: a}
	ctx := command.WithSource(context.Background(), "cli")
	b, _ := json.Marshal(map[string]string{"display": "Reviewer"})
	_, err := tl.Run(ctx, b)
	if err == nil || !strings.Contains(err.Error(), "id is required") {
		t.Fatalf("create_persona without id err = %v, want id-required error", err)
	}
}
