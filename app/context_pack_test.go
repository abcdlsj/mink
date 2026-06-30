package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abcdlsj/sumi/config"
	"github.com/abcdlsj/sumi/space"
)

func TestBuildContextPackIncludesTriggerScopeSummaryAndMemoryRefs(t *testing.T) {
	dir := t.TempDir()
	a, err := New(config.Config{
		Runtime:   "stub",
		DataDir:   filepath.Join(dir, "sumi-data"),
		Workspace: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })

	ch, err := a.Spaces().EnsureSpace(space.KindChannel, "work", space.PersonaInfo{})
	if err != nil {
		t.Fatal(err)
	}
	root, err := a.Spaces().AppendUserMessage(ch.ID, "please investigate the routing noise", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Spaces().AppendMessageWithRouting(ch.ID, space.Message{
		AuthorID:   "reviewer",
		AuthorKind: space.ParticipantAgent,
		Content:    "I think the current wake path is too broad.",
	}, nil, nil); err != nil {
		t.Fatal(err)
	}

	memDir := filepath.Join(a.Config().MemoryDir(), "persona", "coder")
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(memDir, "pref-1.md"), []byte(`---
title: "Reply style"
summary: "Keep routing audits concise."
kind: "preference"
updated_at: 2026-06-30T00:00:00Z
---

# Reply style

Keep routing audits concise.
`), 0o644); err != nil {
		t.Fatal(err)
	}

	pack := a.BuildContextPack(ContextPackInput{
		SpaceID: ch.ID,
		AgentID: "coder",
	})
	if len(pack.Segments) < 3 {
		t.Fatalf("segments = %#v, want trigger + scope summary + memory ref", pack.Segments)
	}
	if pack.Segments[0].Kind != "trigger" || pack.Segments[0].RefID != root.ID {
		t.Fatalf("trigger segment = %#v", pack.Segments[0])
	}
	if !strings.Contains(pack.Segments[0].Summary, "please investigate the routing noise") {
		t.Fatalf("trigger summary = %q", pack.Segments[0].Summary)
	}
	if pack.Segments[1].Kind != "scope_summary" {
		t.Fatalf("scope segment = %#v", pack.Segments[1])
	}
	if !strings.Contains(pack.Segments[1].Summary, "profile=channel") {
		t.Fatalf("scope summary = %q", pack.Segments[1].Summary)
	}
	var foundMemory bool
	for _, seg := range pack.Segments {
		if seg.Kind != "memory_ref" {
			continue
		}
		foundMemory = true
		if seg.RefID != "persona:coder:pref-1" {
			t.Fatalf("memory ref id = %q", seg.RefID)
		}
		if !strings.Contains(seg.Summary, "routing audits concise") {
			t.Fatalf("memory summary = %q", seg.Summary)
		}
	}
	if !foundMemory {
		t.Fatalf("segments = %#v, want memory_ref", pack.Segments)
	}
}

func TestBuildContextPackThreadUsesThreadScope(t *testing.T) {
	dir := t.TempDir()
	a, err := New(config.Config{
		Runtime:   "stub",
		DataDir:   filepath.Join(dir, "sumi-data"),
		Workspace: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })

	ch, err := a.Spaces().EnsureSpace(space.KindChannel, "work", space.PersonaInfo{})
	if err != nil {
		t.Fatal(err)
	}
	root, err := a.Spaces().AppendUserMessage(ch.ID, "root", nil)
	if err != nil {
		t.Fatal(err)
	}
	threadMsg, err := a.Spaces().AppendUserMessageInThread(ch.ID, root.ID, "thread-specific ask", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Spaces().AppendMessageWithRouting(ch.ID, space.Message{
		AuthorID:        "reviewer",
		AuthorKind:      space.ParticipantAgent,
		Content:         "thread-only context",
		ParentMessageID: root.ID,
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Spaces().AppendMessageWithRouting(ch.ID, space.Message{
		AuthorID:   "reviewer",
		AuthorKind: space.ParticipantAgent,
		Content:    "top-level context should stay out",
	}, nil, nil); err != nil {
		t.Fatal(err)
	}

	pack := a.BuildContextPack(ContextPackInput{
		SpaceID:         ch.ID,
		ParentMessageID: root.ID,
		AgentID:         "coder",
	})
	if len(pack.Segments) < 2 {
		t.Fatalf("segments = %#v", pack.Segments)
	}
	if pack.Segments[0].RefID != threadMsg.ID {
		t.Fatalf("thread trigger = %#v, want ref %q", pack.Segments[0], threadMsg.ID)
	}
	if pack.Segments[1].Title != "Thread Summary" {
		t.Fatalf("thread scope title = %q", pack.Segments[1].Title)
	}
	if strings.Contains(pack.Segments[1].Summary, "top-level context should stay out") {
		t.Fatalf("thread summary leaked top-level context: %q", pack.Segments[1].Summary)
	}
	if !strings.Contains(pack.Segments[1].Summary, "thread-only context") {
		t.Fatalf("thread summary missing thread context: %q", pack.Segments[1].Summary)
	}
}
