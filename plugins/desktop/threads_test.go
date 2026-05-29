package desktop

import (
	"path/filepath"
	"testing"

	"github.com/abcdlsj/sumi/app"
	"github.com/abcdlsj/sumi/config"
	"github.com/abcdlsj/sumi/persona"
	"github.com/abcdlsj/sumi/space"
	taskpkg "github.com/abcdlsj/sumi/task"
)

func newThreadBackend(t *testing.T) (*Backend, *app.App) {
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
	if _, err := a.Personas().Create("coder", persona.Meta{Display: "Coder", Runtime: "stub"}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Personas().Create("reviewer", persona.Meta{Display: "Reviewer", Runtime: "stub"}, ""); err != nil {
		t.Fatal(err)
	}
	return newBackend(a), a
}

func mustChannel(t *testing.T, a *app.App) *space.Space {
	t.Helper()
	sp, err := a.Spaces().EnsureSpace(space.KindChannel, "default", space.PersonaInfo{})
	if err != nil {
		t.Fatal(err)
	}
	return sp
}

func TestListThreadsForSpaceSkipsRootsWithoutReplies(t *testing.T) {
	b, a := newThreadBackend(t)
	sp := mustChannel(t, a)
	root, err := a.Spaces().AppendUserMessage(sp.ID, "no one replies", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = root
	if _, err := a.Spaces().AppendUserMessage(sp.ID, "another root", nil); err != nil {
		t.Fatal(err)
	}
	if got := b.ListThreadsForSpace(sp.ID); len(got) != 0 {
		t.Fatalf("len = %d, want 0 (no replies = no threads)", len(got))
	}
}

func TestListThreadsForSpaceReturnsRootWithReplies(t *testing.T) {
	b, a := newThreadBackend(t)
	sp := mustChannel(t, a)
	root, err := a.Spaces().AppendUserMessage(sp.ID, "kick off the audit", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Spaces().AppendAgentMessage(sp.ID,
		space.PersonaInfo{ID: "coder", Display: "Coder"},
		"first finding", "", nil, root.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Spaces().AppendAgentMessage(sp.ID,
		space.PersonaInfo{ID: "reviewer", Display: "Reviewer"},
		"second finding", "", nil, root.ID); err != nil {
		t.Fatal(err)
	}
	got := b.ListThreadsForSpace(sp.ID)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].ParentID != root.ID {
		t.Fatalf("parent = %q", got[0].ParentID)
	}
	if got[0].ReplyCount != 2 {
		t.Fatalf("reply count = %d, want 2", got[0].ReplyCount)
	}
	if got[0].LastReplyAuthor != "Reviewer" {
		t.Fatalf("last reply author = %q, want Reviewer", got[0].LastReplyAuthor)
	}
}

func TestGetThreadDetailReturnsParentRepliesAndParticipants(t *testing.T) {
	b, a := newThreadBackend(t)
	sp := mustChannel(t, a)
	root, err := a.Spaces().AppendUserMessage(sp.ID, "audit retry", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Spaces().AppendAgentMessage(sp.ID,
		space.PersonaInfo{ID: "coder", Display: "Coder"},
		"checking", "", nil, root.ID); err != nil {
		t.Fatal(err)
	}
	detail := b.GetThreadDetail(sp.ID, root.ID)
	if detail.NotFound || detail.Unsupported {
		t.Fatalf("detail = %+v", detail)
	}
	if detail.Parent == nil || detail.Parent.ID != root.ID {
		t.Fatalf("parent = %+v", detail.Parent)
	}
	if len(detail.Replies) != 1 || detail.Replies[0].AuthorID != "coder" {
		t.Fatalf("replies = %+v", detail.Replies)
	}
	authorIDs := map[string]bool{}
	for _, p := range detail.Participants {
		authorIDs[p.ID] = true
	}
	if !authorIDs["user"] {
		t.Fatalf("participants must include root author 'user', got %+v", detail.Participants)
	}
	if !authorIDs["coder"] {
		t.Fatalf("participants must include reply author 'coder'")
	}
}

func TestGetThreadDetailNotFoundForUnknownParent(t *testing.T) {
	b, a := newThreadBackend(t)
	sp := mustChannel(t, a)
	detail := b.GetThreadDetail(sp.ID, "msg-does-not-exist")
	if !detail.NotFound {
		t.Fatalf("expected NotFound=true, got %+v", detail)
	}
	if detail.Unsupported {
		t.Fatal("NotFound and Unsupported must not both be true")
	}
}

func TestGetThreadDetailUnsupportedForAgentDM(t *testing.T) {
	b, a := newThreadBackend(t)
	sp, err := a.Spaces().EnsureSpace(space.KindAgentDM, "coder", space.PersonaInfo{ID: "coder", Display: "Coder"})
	if err != nil {
		t.Fatal(err)
	}
	root, err := a.Spaces().AppendUserMessage(sp.ID, "hi", nil)
	if err != nil {
		t.Fatal(err)
	}
	detail := b.GetThreadDetail(sp.ID, root.ID)
	if !detail.Unsupported {
		t.Fatalf("expected Unsupported=true for AgentDM, got %+v", detail)
	}
	if detail.NotFound {
		t.Fatal("Unsupported and NotFound must not both be true")
	}
	if detail.UnsupportedHint == "" {
		t.Fatal("UnsupportedHint should explain why")
	}
}

func TestListThreadsForSpaceReturnsNothingForAgentDM(t *testing.T) {
	b, a := newThreadBackend(t)
	sp, err := a.Spaces().EnsureSpace(space.KindAgentDM, "coder", space.PersonaInfo{ID: "coder", Display: "Coder"})
	if err != nil {
		t.Fatal(err)
	}
	root, err := a.Spaces().AppendUserMessage(sp.ID, "kick", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Spaces().AppendAgentMessage(sp.ID,
		space.PersonaInfo{ID: "coder", Display: "Coder"},
		"reply", "", nil, root.ID); err != nil {
		t.Fatal(err)
	}
	if got := b.ListThreadsForSpace(sp.ID); len(got) != 0 {
		t.Fatalf("AgentDM must not enumerate threads; got %d", len(got))
	}
}

func TestThreadRunsScopeIncludesRootAndReplies(t *testing.T) {
	b, a := newThreadBackend(t)
	sp := mustChannel(t, a)
	root, err := a.Spaces().AppendUserMessage(sp.ID, "kick", nil)
	if err != nil {
		t.Fatal(err)
	}
	reply, err := a.Spaces().AppendAgentMessage(sp.ID,
		space.PersonaInfo{ID: "coder", Display: "Coder"},
		"first", "", nil, root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Tasks().Create(taskpkg.CreateTaskInput{
		SpaceID: sp.ID, TriggerMessageID: root.ID, InitiatorID: "user", WorkerID: "coder", Title: "from root",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Tasks().Create(taskpkg.CreateTaskInput{
		SpaceID: sp.ID, TriggerMessageID: reply.ID, InitiatorID: "user", WorkerID: "reviewer", Title: "from reply",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Tasks().Create(taskpkg.CreateTaskInput{
		SpaceID: sp.ID, TriggerMessageID: "msg-other", InitiatorID: "user", WorkerID: "coder", Title: "outside thread",
	}); err != nil {
		t.Fatal(err)
	}
	detail := b.GetThreadDetail(sp.ID, root.ID)
	if len(detail.RecentRuns) != 2 {
		t.Fatalf("RecentRuns = %d, want 2 (root trigger + reply trigger only)", len(detail.RecentRuns))
	}
	for _, r := range detail.RecentRuns {
		if r.Title == "outside thread" {
			t.Fatal("thread runs leaked an outside-thread task")
		}
	}
}

func TestThreadHasRunningWorkerWhenTaskRunning(t *testing.T) {
	b, a := newThreadBackend(t)
	sp := mustChannel(t, a)
	root, err := a.Spaces().AppendUserMessage(sp.ID, "kick", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Spaces().AppendAgentMessage(sp.ID,
		space.PersonaInfo{ID: "coder", Display: "Coder"},
		"working", "", nil, root.ID); err != nil {
		t.Fatal(err)
	}
	tk, err := a.Tasks().Create(taskpkg.CreateTaskInput{
		SpaceID: sp.ID, TriggerMessageID: root.ID, InitiatorID: "user", WorkerID: "coder", Title: "live",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Tasks().Update(tk.ID, taskpkg.UpdateTaskInput{Status: taskpkg.StatusRunning}); err != nil {
		t.Fatal(err)
	}
	got := b.ListThreadsForSpace(sp.ID)
	if len(got) != 1 || !got[0].HasRunningWorker {
		t.Fatalf("HasRunningWorker not set: %+v", got)
	}
	detail := b.GetThreadDetail(sp.ID, root.ID)
	if detail.ActiveWorkerID != "coder" {
		t.Fatalf("ActiveWorkerID = %q, want coder", detail.ActiveWorkerID)
	}
}
