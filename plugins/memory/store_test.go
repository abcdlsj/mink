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

func TestMemoryProposalRequiresConfirmBeforeSearch(t *testing.T) {
	s, err := open(t.TempDir(), "/workspace")
	if err != nil {
		t.Fatal(err)
	}
	ctx := command.WithSource(context.Background(), "desktop:channel:alpha")
	ctx = command.WithPersona(ctx, "bob")
	ctx = command.WithParentMessage(ctx, "msg-123")

	out, err := (&proposeTool{s: s}).Run(ctx, mustMemoryJSON(t, proposeArgs{
		Title:      "Reply style",
		Content:    "The user prefers direct Chinese replies.",
		Kind:       "preference",
		Reason:     "The user explicitly requested this communication style.",
		Confidence: "high",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "memory proposal memprop-") || !strings.Contains(out, "!memory confirm") {
		t.Fatalf("proposal output = %q", out)
	}

	before, err := s.search(ctx, []scope{{Kind: "persona", Key: "bob"}}, "direct Chinese", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 0 {
		t.Fatalf("unconfirmed proposal leaked into search: %#v", before)
	}

	items, err := s.listProposals()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("proposals = %d, want 1", len(items))
	}
	if items[0].ScopeKind != "persona" || items[0].ScopeKey != "bob" || items[0].SourceMessageID != "msg-123" || items[0].Confidence != "high" {
		t.Fatalf("proposal metadata = %#v", items[0])
	}

	doc, err := s.confirmProposal(ctx, items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if doc.ScopeKind != "persona" || doc.ScopeKey != "bob" || doc.CreatedBy != "bob" || doc.Confidence != "high" {
		t.Fatalf("confirmed doc = %#v", doc)
	}

	after, err := s.search(ctx, []scope{{Kind: "persona", Key: "bob"}}, "direct Chinese", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 1 {
		t.Fatalf("confirmed search docs = %d, want 1", len(after))
	}
}

func TestRememberMemoryRequiresExplicitCurrentUserAuthorization(t *testing.T) {
	s, err := open(t.TempDir(), "/workspace")
	if err != nil {
		t.Fatal(err)
	}
	ctx := command.WithSource(context.Background(), "desktop:channel:alpha")
	ctx = command.WithPersona(ctx, "bob")
	ctx = command.WithRunContext(ctx, command.RunContext{
		Source: "desktop:channel:alpha",
		Input:  "记住我喜欢中文简洁回答",
	})

	out, err := (&rememberTool{s: s}).Run(ctx, mustMemoryJSON(t, rememberArgs{
		Title:             "Reply preference",
		Body:              "The user prefers concise Chinese replies.",
		Kind:              "preference",
		AuthorizationText: "记住我喜欢中文简洁回答",
		Confidence:        "high",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Remembered memory") || !strings.Contains(out, "!memory delete") {
		t.Fatalf("remember output = %q", out)
	}

	docs, err := s.search(ctx, []scope{{Kind: "persona", Key: "bob"}}, "concise Chinese", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 {
		t.Fatalf("remembered docs = %d, want 1", len(docs))
	}
	if docs[0].CreatedBy != "user" || docs[0].Confidence != "high" {
		t.Fatalf("remembered metadata = %#v", docs[0])
	}
}

func TestRememberMemoryRejectsInferredOrSensitiveMemory(t *testing.T) {
	s, err := open(t.TempDir(), "/workspace")
	if err != nil {
		t.Fatal(err)
	}
	ctx := command.WithRunContext(context.Background(), command.RunContext{
		Source: "desktop:channel:alpha",
		Input:  "我们聊一下代码风格",
	})
	ctx = command.WithPersona(ctx, "bob")

	_, err = (&rememberTool{s: s}).Run(ctx, mustMemoryJSON(t, rememberArgs{
		Title:             "Inferred preference",
		Body:              "The user may prefer concise replies.",
		AuthorizationText: "我们聊一下代码风格",
	}))
	if err == nil || !strings.Contains(err.Error(), "explicitly ask to remember") {
		t.Fatalf("inferred memory err = %v", err)
	}

	ctx = command.WithRunContext(context.Background(), command.RunContext{
		Source: "desktop:channel:alpha",
		Input:  "记住我的 token=sk_agent_secret",
	})
	ctx = command.WithPersona(ctx, "bob")
	_, err = (&rememberTool{s: s}).Run(ctx, mustMemoryJSON(t, rememberArgs{
		Title:             "Secret",
		Body:              "token=sk_agent_secret",
		AuthorizationText: "记住我的 token=sk_agent_secret",
	}))
	if err == nil || !strings.Contains(err.Error(), "sensitive") {
		t.Fatalf("sensitive memory err = %v", err)
	}
}

func TestRejectProposalAndDeleteMemory(t *testing.T) {
	s, err := open(t.TempDir(), "/workspace")
	if err != nil {
		t.Fatal(err)
	}
	ctx := command.WithPersona(context.Background(), "bob")

	p, err := s.propose(ctx, scope{Kind: "persona", Key: "bob"}, proposeArgs{
		Title:      "Temp fact",
		Content:    "Temporary fact should not persist.",
		Reason:     "Testing rejection.",
		Confidence: "low",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.rejectProposal(p.ID); err != nil {
		t.Fatal(err)
	}
	items, err := s.listProposals()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("proposals after reject = %#v", items)
	}
	docs, err := s.search(ctx, []scope{{Kind: "persona", Key: "bob"}}, "Temporary fact", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 0 {
		t.Fatalf("rejected proposal persisted: %#v", docs)
	}

	d, err := s.put(ctx, scope{Kind: "persona", Key: "bob"}, memoryDocFromWrite(ctx, writeArgs{
		Title: "Delete me",
		Body:  "This memory should be removed.",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.delete(ctx, scope{Kind: "persona", Key: "bob"}, d.ID); err != nil {
		t.Fatal(err)
	}
	docs, err = s.search(ctx, []scope{{Kind: "persona", Key: "bob"}}, "removed", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 0 {
		t.Fatalf("deleted memory still searchable: %#v", docs)
	}
}

func TestUpdateMemoryPreservesIDAndMetadata(t *testing.T) {
	s, err := open(t.TempDir(), "/workspace")
	if err != nil {
		t.Fatal(err)
	}
	ctx := command.WithSource(context.Background(), "desktop:channel:alpha")
	ctx = command.WithPersona(ctx, "bob")
	ctx = command.WithParentMessage(ctx, "msg-123")

	d, err := s.put(ctx, scope{Kind: "persona", Key: "bob"}, memoryDocFromWrite(ctx, writeArgs{
		Title:         "Project context",
		Body:          "Old memory body.",
		Kind:          "fact",
		SourceSpaceID: "space-1",
		CreatedBy:     "bob",
		Confidence:    "medium",
	}))
	if err != nil {
		t.Fatal(err)
	}
	originalPath := d.Path
	originalCreatedAt := d.CreatedAt

	out, err := (&updateTool{s: s}).Run(ctx, mustMemoryJSON(t, updateArgs{
		ScopeKind:  "persona",
		ScopeKey:   "bob",
		ID:         d.ID,
		Title:      "Renamed project context",
		Body:       "Updated memory body.",
		Kind:       "decision",
		Confidence: "high",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "updated memory "+d.ID) {
		t.Fatalf("update output = %q", out)
	}

	docs, err := s.recent(ctx, scope{Kind: "persona", Key: "bob"}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 {
		t.Fatalf("docs = %d, want 1", len(docs))
	}
	updated := docs[0]
	if updated.ID != d.ID || updated.Path != originalPath {
		t.Fatalf("updated doc moved or changed id: before id=%q path=%q after=%#v", d.ID, originalPath, updated)
	}
	if updated.CreatedAt != originalCreatedAt || updated.SourceMessageID != "msg-123" || updated.SourceSpaceID != "space-1" || updated.CreatedBy != "bob" {
		t.Fatalf("metadata not preserved: %#v", updated)
	}
	if updated.Title != "Renamed project context" || !strings.Contains(updated.Body, "Updated memory body.") || updated.Kind != "decision" || updated.Confidence != "high" {
		t.Fatalf("updated fields wrong: %#v", updated)
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
