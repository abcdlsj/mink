package sqlite_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	sqlite "github.com/abcdlsj/mink/runtime/store/sqlite"
)

func testDB(t *testing.T) *sqlite.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := sqlite.Open(filepath.Join(dir, "test.db"), sqlite.OpenOptions{PoolSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestTaskStateMachine(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	state, err := db.StartRun(ctx, "cli:test", "sess1", "agent:main", "user_input", "hello world")
	if err != nil {
		t.Fatal(err)
	}
	if state.TaskID == "" || state.RunID == "" {
		t.Fatal("expected non-empty task and run IDs")
	}

	task, err := db.GetTask(ctx, state.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != sqlite.TaskRunning {
		t.Fatalf("expected running, got %s", task.Status)
	}

	if err := db.FinishRun(ctx, state, nil); err != nil {
		t.Fatal(err)
	}
	task, err = db.GetTask(ctx, state.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != sqlite.TaskWaiting {
		t.Fatalf("expected waiting, got %s", task.Status)
	}

	state2, err := db.ResumeTask(ctx, state.TaskID, "agent:main", "sess2", "cli:test")
	if err != nil {
		t.Fatal(err)
	}
	if state2.RunID == state.RunID {
		t.Fatal("expected new run ID")
	}
	task, err = db.GetTask(ctx, state.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != sqlite.TaskRunning {
		t.Fatalf("expected running after resume, got %s", task.Status)
	}

	if err := db.FinishRun(ctx, state2, fmt.Errorf("something broke")); err != nil {
		t.Fatal(err)
	}
	task, err = db.GetTask(ctx, state.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != sqlite.TaskFailed {
		t.Fatalf("expected failed, got %s", task.Status)
	}

	state3, err := db.StartRun(ctx, "cli:test", "sess3", "agent:main", "user_input", "retry")
	if err != nil {
		t.Fatal(err)
	}
	if state3.TaskID == state.TaskID {
		t.Fatal("expected new task ID for failed task source")
	}

	if err := db.FinishRun(ctx, state3, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.CompleteTask(ctx, state3.TaskID); err != nil {
		t.Fatal(err)
	}
	task, err = db.GetTask(ctx, state3.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != sqlite.TaskDone {
		t.Fatalf("expected done, got %s", task.Status)
	}
}

func TestChildTask(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	parent, err := db.StartRun(ctx, "cli:parent", "sess1", "agent:main", "user_input", "parent task")
	if err != nil {
		t.Fatal(err)
	}

	childID, err := db.CreateChildTask(ctx, parent.TaskID, "delegation", "child work", "agent:sub", "delegate:cli:parent:123")
	if err != nil {
		t.Fatal(err)
	}
	if childID == "" {
		t.Fatal("expected non-empty child task ID")
	}

	child, err := db.GetTask(ctx, childID)
	if err != nil {
		t.Fatal(err)
	}
	if child.ParentTaskID != parent.TaskID {
		t.Fatalf("expected parent %s, got %s", parent.TaskID, child.ParentTaskID)
	}
	if child.Status != sqlite.TaskQueued {
		t.Fatalf("expected queued, got %s", child.Status)
	}

	children, err := db.ListTasks(ctx, sqlite.TaskListOptions{ParentTaskID: parent.TaskID})
	if err != nil {
		t.Fatal(err)
	}
	if len(children) != 1 {
		t.Fatalf("expected 1 child, got %d", len(children))
	}
}

func TestListTasks(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	s1, _ := db.StartRun(ctx, "cli:a", "s1", "agent:main", "user_input", "task a")
	db.FinishRun(ctx, s1, nil)

	s2, _ := db.StartRun(ctx, "cli:b", "s2", "agent:main", "user_input", "task b")
	_ = s2

	all, err := db.ListTasks(ctx, sqlite.TaskListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) < 2 {
		t.Fatalf("expected at least 2 tasks, got %d", len(all))
	}

	waiting, err := db.ListTasks(ctx, sqlite.TaskListOptions{Status: sqlite.TaskWaiting})
	if err != nil {
		t.Fatal(err)
	}
	if len(waiting) != 1 {
		t.Fatalf("expected 1 waiting task, got %d", len(waiting))
	}
}

func TestRecoveryAndResume(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db1, err := sqlite.Open(dbPath, sqlite.OpenOptions{PoolSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	state, err := db1.StartRun(ctx, "cli:crash", "sess1", "agent:main", "user_input", "will crash")
	if err != nil {
		t.Fatal(err)
	}
	db1.Close()

	db2, err := sqlite.Open(dbPath, sqlite.OpenOptions{PoolSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()

	if err := db2.Recover(ctx); err != nil {
		t.Fatal(err)
	}

	task, err := db2.GetTask(ctx, state.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != sqlite.TaskWaiting {
		t.Fatalf("expected waiting after recovery, got %s", task.Status)
	}

	state2, err := db2.ResumeTask(ctx, state.TaskID, "agent:main", "sess2", "cli:crash")
	if err != nil {
		t.Fatal(err)
	}
	if state2.TaskID != state.TaskID {
		t.Fatal("expected same task ID on resume")
	}
}

func TestWorkspaceScopedRuntimeState(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	ctx := context.Background()

	dbA, err := sqlite.Open(dbPath, sqlite.OpenOptions{PoolSize: 1, Workspace: "/tmp/ws-a"})
	if err != nil {
		t.Fatal(err)
	}
	defer dbA.Close()

	state, err := dbA.StartRun(ctx, "cli:shared", "sess-a", "agent:main", "user_input", "hello from a")
	if err != nil {
		t.Fatal(err)
	}

	bindingsA, err := dbA.SessionBindings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if bindingsA["cli:shared"] != "sess-a" {
		t.Fatalf("expected workspace a binding, got %#v", bindingsA)
	}

	dbB, err := sqlite.Open(dbPath, sqlite.OpenOptions{PoolSize: 1, Workspace: "/tmp/ws-b"})
	if err != nil {
		t.Fatal(err)
	}
	defer dbB.Close()

	bindingsB, err := dbB.SessionBindings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(bindingsB) != 0 {
		t.Fatalf("expected no bindings for workspace b, got %#v", bindingsB)
	}

	tasksB, err := dbB.ListTasks(ctx, sqlite.TaskListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasksB) != 0 {
		t.Fatalf("expected no tasks for workspace b, got %#v", tasksB)
	}

	if err := dbB.Recover(ctx); err != nil {
		t.Fatal(err)
	}

	task, err := dbA.GetTask(ctx, state.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != sqlite.TaskRunning {
		t.Fatalf("expected workspace a task to stay running after workspace b recovery, got %s", task.Status)
	}

	if err := dbA.Recover(ctx); err != nil {
		t.Fatal(err)
	}

	task, err = dbA.GetTask(ctx, state.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != sqlite.TaskWaiting {
		t.Fatalf("expected workspace a task to recover to waiting, got %s", task.Status)
	}
}

func TestSessionDerivation(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	state, err := db.StartRun(ctx, "cli:derive", "sess1", "agent:main", "user_input", "test derive")
	if err != nil {
		t.Fatal(err)
	}

	db.AppendEvent(ctx, sqlite.Event{
		TaskID: state.TaskID, RunID: state.RunID,
		Type: "input.received", ActorType: "user", ActorID: "user1",
		Source: "cli:derive", Payload: map[string]any{"content": "hello"},
	})
	db.AppendEvent(ctx, sqlite.Event{
		TaskID: state.TaskID, RunID: state.RunID,
		Type: "assistant.emitted", ActorType: "assistant", ActorID: "agent:main",
		Source: "cli:derive", Payload: map[string]any{"content": "hi there", "agent_id": "agent:main"},
	})
	db.AppendEvent(ctx, sqlite.Event{
		TaskID: state.TaskID, RunID: state.RunID,
		Type: "tool.called", ActorType: "tool", ActorID: "agent:main",
		Source: "cli:derive", Payload: map[string]any{"id": "tc1", "name": "search", "args": `{"q":"test"}`},
	})
	db.AppendEvent(ctx, sqlite.Event{
		TaskID: state.TaskID, RunID: state.RunID,
		Type: "tool.completed", ActorType: "tool", ActorID: "agent:main",
		Source: "cli:derive", Payload: map[string]any{"id": "tc1", "name": "search", "output": "found it"},
	})

	msgs, err := db.MessagesForSource(ctx, "cli:derive", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) < 3 {
		t.Fatalf("expected at least 3 messages, got %d", len(msgs))
	}

	taskMsgs, err := db.MessagesForTask(ctx, state.TaskID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(taskMsgs) != len(msgs) {
		t.Fatalf("task msgs (%d) != source msgs (%d)", len(taskMsgs), len(msgs))
	}

	runMsgs, err := db.MessagesForRun(ctx, state.RunID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(runMsgs) != len(msgs) {
		t.Fatalf("run msgs (%d) != source msgs (%d)", len(runMsgs), len(msgs))
	}

	for _, m := range taskMsgs {
		if m.Role == "assistant" && m.Content != "" && m.AgentID != "agent:main" {
			t.Fatalf("expected agent_id agent:main, got %s", m.AgentID)
		}
	}
}

func TestTeamSourceBindings(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	if err := db.UpsertTeamSourceBinding(ctx, "cli", "team-1", "thread-1"); err != nil {
		t.Fatal(err)
	}

	bindings, err := db.TeamSourceBindings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 1 {
		t.Fatalf("expected 1 binding, got %d", len(bindings))
	}
	if bindings[0].Source != "cli" || bindings[0].TeamID != "team-1" || bindings[0].ThreadID != "thread-1" {
		t.Fatalf("unexpected binding: %+v", bindings[0])
	}

	if err := db.ClearTeamSourceBinding(ctx, "cli"); err != nil {
		t.Fatal(err)
	}
	bindings, err = db.TeamSourceBindings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 0 {
		t.Fatalf("expected no bindings after clear, got %d", len(bindings))
	}
}

func TestTeamCRUD(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	teamID, err := db.CreateTeam(ctx, "test-team", "agent:leader", "leader_driven", 6)
	if err != nil {
		t.Fatal(err)
	}
	if teamID == "" {
		t.Fatal("expected non-empty team ID")
	}

	team, err := db.GetTeam(ctx, teamID)
	if err != nil {
		t.Fatal(err)
	}
	if team.Name != "test-team" {
		t.Fatalf("expected test-team, got %s", team.Name)
	}
	if team.LeaderAgentID != "agent:leader" {
		t.Fatalf("expected agent:leader, got %s", team.LeaderAgentID)
	}

	members, err := db.ListTeamMembers(ctx, teamID)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 1 {
		t.Fatalf("expected 1 member (leader), got %d", len(members))
	}
	if members[0].RoleName != "leader" {
		t.Fatalf("expected leader role, got %s", members[0].RoleName)
	}

	if err := db.AddTeamMember(ctx, teamID, "agent:worker", "coder", "writes code", "persistent"); err != nil {
		t.Fatal(err)
	}
	members, err = db.ListTeamMembers(ctx, teamID)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 2 {
		t.Fatalf("expected 2 members, got %d", len(members))
	}
	if members[1].RuntimeAgentID != "agent:worker" {
		t.Fatalf("expected runtime agent agent:worker, got %s", members[1].RuntimeAgentID)
	}

	teams, err := db.ListTeams(ctx, "active")
	if err != nil {
		t.Fatal(err)
	}
	if len(teams) != 1 {
		t.Fatalf("expected 1 team, got %d", len(teams))
	}

	if err := db.UpdateTeamTurnPolicy(ctx, teamID, "round_robin"); err != nil {
		t.Fatal(err)
	}
	team, err = db.GetTeam(ctx, teamID)
	if err != nil {
		t.Fatal(err)
	}
	if team.TurnPolicy != "round_robin" {
		t.Fatalf("expected round_robin, got %s", team.TurnPolicy)
	}

	if err := db.RemoveTeamMember(ctx, teamID, "agent:worker"); err != nil {
		t.Fatal(err)
	}
	members, err = db.ListTeamMembers(ctx, teamID)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 1 {
		t.Fatalf("expected 1 member after removal, got %d", len(members))
	}
}

func TestTeamMemberProfile(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	teamID, err := db.CreateTeam(ctx, "profile-team", "agent:leader", "round_robin", 6)
	if err != nil {
		t.Fatal(err)
	}
	err = db.AddTeamMemberWithProfile(ctx, teamID, "agent:team:dev:analyst", "Analyst", "Investigates runtime issues", "ephemeral", sqlite.TeamMemberProfile{
		RuntimeAgentID: "agent:coder",
		ProfileHint:    "debugger",
	})
	if err != nil {
		t.Fatal(err)
	}

	members, err := db.ListTeamMembers(ctx, teamID)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 2 {
		t.Fatalf("expected 2 members, got %d", len(members))
	}
	member := members[1]
	if member.RuntimeAgentID != "agent:coder" {
		t.Fatalf("expected agent:coder, got %s", member.RuntimeAgentID)
	}
	if member.ProfileHint != "debugger" {
		t.Fatalf("expected debugger, got %s", member.ProfileHint)
	}
}

func TestThreadCRUD(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	teamID, _ := db.CreateTeam(ctx, "thread-team", "agent:leader", "", 0)

	threadID, err := db.CreateThread(ctx, teamID, "design discussion", "sess1")
	if err != nil {
		t.Fatal(err)
	}
	if threadID == "" {
		t.Fatal("expected non-empty thread ID")
	}

	thread, err := db.GetThread(ctx, threadID)
	if err != nil {
		t.Fatal(err)
	}
	if thread.Title != "design discussion" {
		t.Fatalf("expected 'design discussion', got %s", thread.Title)
	}
	if thread.Status != "active" {
		t.Fatalf("expected active, got %s", thread.Status)
	}

	round, err := db.IncrementThreadRound(ctx, threadID)
	if err != nil {
		t.Fatal(err)
	}
	if round != 1 {
		t.Fatalf("expected round 1, got %d", round)
	}

	threads, err := db.ListThreads(ctx, teamID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 1 {
		t.Fatalf("expected 1 thread, got %d", len(threads))
	}
}

func TestAgentIdentity(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	if err := db.UpsertAgentIdentity(ctx, "agent:coder", "Coder", "writes clean Go code", "team:dev"); err != nil {
		t.Fatal(err)
	}

	identity, err := db.GetAgentIdentity(ctx, "agent:coder")
	if err != nil {
		t.Fatal(err)
	}
	if identity.DisplayName != "Coder" {
		t.Fatalf("expected Coder, got %s", identity.DisplayName)
	}
	if identity.Profile != "writes clean Go code" {
		t.Fatalf("expected profile, got %s", identity.Profile)
	}

	if err := db.UpsertAgentIdentity(ctx, "agent:coder", "Senior Coder", "writes clean Go code", "team:dev"); err != nil {
		t.Fatal(err)
	}
	identity, err = db.GetAgentIdentity(ctx, "agent:coder")
	if err != nil {
		t.Fatal(err)
	}
	if identity.DisplayName != "Senior Coder" {
		t.Fatalf("expected Senior Coder after update, got %s", identity.DisplayName)
	}

	agents, err := db.ListAgentIdentities(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(agents))
	}
}

func TestCompactAndReplay(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	state, err := db.StartRun(ctx, "cli:compact", "sess1", "agent:main", "user_input", "compact test")
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 5; i++ {
		db.AppendEvent(ctx, sqlite.Event{
			TaskID: state.TaskID, RunID: state.RunID,
			Type: "input.received", ActorType: "user",
			Source: "cli:compact", Payload: map[string]any{"content": fmt.Sprintf("msg %d", i)},
		})
		db.AppendEvent(ctx, sqlite.Event{
			TaskID: state.TaskID, RunID: state.RunID,
			Type: "assistant.emitted", ActorType: "assistant",
			Source: "cli:compact", Payload: map[string]any{"content": fmt.Sprintf("reply %d", i)},
		})
	}

	if err := db.CompactSource(ctx, "cli:compact", "summary of conversation", "note"); err != nil {
		t.Fatal(err)
	}

	msgs, err := db.MessagesForSource(ctx, "cli:compact", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message after compact, got %d", len(msgs))
	}
	if msgs[0].Role != "system" {
		t.Fatalf("expected system role, got %s", msgs[0].Role)
	}
}

var _ = os.DevNull
