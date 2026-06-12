package memory

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abcdlsj/sumi/command"
)

func TestWriteMemoryUsesScopedPersonaStoreWithMetadata(t *testing.T) {
	s, err := open(t.TempDir(), "/workspace")
	if err != nil {
		t.Fatal(err)
	}
	ctx := command.WithSource(context.Background(), "desktop:channel:alpha")
	ctx = command.WithPersona(ctx, "bob")
	ctx = command.WithParentMessage(ctx, "msg-123")

	_, err = (&writeTool{s: s}).Run(ctx, mustMemoryJSON(t, writeArgs{
		Title:         "User preference",
		Body:          "The user prefers concise Chinese replies.",
		Kind:          "preference",
		Confidence:    "high",
		SourceSpaceID: "space-1",
	}))
	if err != nil {
		t.Fatal(err)
	}

	docs, err := s.recent(ctx, scope{Kind: "persona", Key: "bob"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 {
		t.Fatalf("docs = %d, want 1", len(docs))
	}
	doc := docs[0]
	wantDir := filepath.Join(s.root, "persona", "bob")
	if filepath.Dir(doc.Path) != wantDir {
		t.Fatalf("path = %q, want dir %q", doc.Path, wantDir)
	}
	if doc.ScopeKind != "persona" || doc.ScopeKey != "bob" || doc.Source != "desktop:channel:alpha" {
		t.Fatalf("scope/source metadata = %#v", doc)
	}
	if doc.SourceMessageID != "msg-123" || doc.SourceSpaceID != "space-1" || doc.CreatedBy != "bob" || doc.Confidence != "high" {
		t.Fatalf("memory metadata = %#v", doc)
	}

	body, err := os.ReadFile(doc.Path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`scope_kind: "persona"`,
		`scope_key: "bob"`,
		`source_message_id: "msg-123"`,
		`source_space_id: "space-1"`,
		`created_by: "bob"`,
		`confidence: "high"`,
		"created_at:",
		"updated_at:",
	} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("memory file missing %q:\n%s", want, body)
		}
	}
}

func mustMemoryJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
