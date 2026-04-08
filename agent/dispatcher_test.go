package agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/session"
)

func TestDispatcherTeamInviteReq(t *testing.T) {
	ctx := context.Background()
	eventBus := bus.New()
	db := testTeamDB(t)
	sm := session.NewManager(session.NewFileStore(filepath.Join(t.TempDir(), "sessions")), eventBus)

	disp := NewDispatcher(AgentDeps{Bus: eventBus, RuntimeDB: db}, sm, nil, db)
	disp.Start(ctx)

	teamID, err := db.CreateTeam(ctx, "invite-team", bus.AddrAgentMain, "leader_driven", 6)
	if err != nil {
		t.Fatal(err)
	}
	threadID, err := db.CreateThread(ctx, teamID, "invite thread", "sess1")
	if err != nil {
		t.Fatal(err)
	}
	disp.BindTeamSource(bus.AddrPlatformCLI, teamID, threadID)

	resp, err := eventBus.Req(ctx, bus.Msg{
		Type: bus.TypeTeamInvite,
		From: bus.AddrAgentMain,
		To:   bus.AddrAgentMain,
		Payload: map[string]string{
			"source":           bus.AddrPlatformCLI,
			"agent_id":         "agent:coder",
			"role_name":        "Coder",
			"role_description": "Writes code",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ack, ok := resp.Payload.(map[string]string)
	if !ok {
		t.Fatalf("unexpected payload: %#v", resp.Payload)
	}
	if ack["status"] != "ok" {
		t.Fatalf("unexpected ack: %#v", ack)
	}

	members, err := db.ListTeamMembers(ctx, teamID)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 2 {
		t.Fatalf("expected 2 members, got %d", len(members))
	}
}

func TestDispatcherTeamSpawnReq(t *testing.T) {
	ctx := context.Background()
	eventBus := bus.New()
	db := testTeamDB(t)
	sm := session.NewManager(session.NewFileStore(filepath.Join(t.TempDir(), "sessions")), eventBus)

	disp := NewDispatcher(AgentDeps{Bus: eventBus, RuntimeDB: db}, sm, nil, db)
	disp.SetRegistry(NewRegistry())
	if err := disp.Registry().Register(AgentDescriptor{
		ID:           "agent:coder",
		Name:         "Coder",
		Capabilities: []string{"code", "go"},
	}); err != nil {
		t.Fatal(err)
	}
	disp.Start(ctx)

	teamID, err := db.CreateTeam(ctx, "spawn-team", bus.AddrAgentMain, "leader_driven", 6)
	if err != nil {
		t.Fatal(err)
	}
	threadID, err := db.CreateThread(ctx, teamID, "spawn thread", "sess1")
	if err != nil {
		t.Fatal(err)
	}
	disp.BindTeamSource(bus.AddrPlatformCLI, teamID, threadID)

	resp, err := eventBus.Req(ctx, bus.Msg{
		Type: bus.TypeTeamSpawn,
		From: bus.AddrAgentMain,
		To:   bus.AddrAgentMain,
		Payload: map[string]any{
			"source":           bus.AddrPlatformCLI,
			"role_name":        "Go Analyst",
			"role_description": "Investigates Go runtime issues",
			"capabilities":     []string{"go"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ack, ok := resp.Payload.(map[string]string)
	if !ok {
		t.Fatalf("unexpected payload: %#v", resp.Payload)
	}
	if ack["status"] != "ok" {
		t.Fatalf("unexpected ack: %#v", ack)
	}
	if ack["runtime_agent_id"] != "agent:coder" {
		t.Fatalf("expected runtime agent agent:coder, got %s", ack["runtime_agent_id"])
	}

	members, err := db.ListTeamMembers(ctx, teamID)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 2 {
		t.Fatalf("expected 2 members, got %d", len(members))
	}
	if members[1].RuntimeAgentID != "agent:coder" {
		t.Fatalf("expected team member runtime agent agent:coder, got %s", members[1].RuntimeAgentID)
	}
}

func TestResolveSpecialistRuntimeAgentFallsBackToMain(t *testing.T) {
	disp := NewDispatcher(AgentDeps{}, nil, nil, nil)
	runtimeAgentID, err := disp.resolveSpecialistRuntimeAgent("", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if runtimeAgentID != bus.AddrAgentMain {
		t.Fatalf("expected %s, got %s", bus.AddrAgentMain, runtimeAgentID)
	}
}

func TestTeamSpecialistPromptIncludesProfileHint(t *testing.T) {
	prompt := teamSpecialistPrompt(TeamTurn{
		SpeakerRole:     "Analyst",
		SpeakerRoleDesc: "Investigates runtime issues",
		SpeakerProfile:  "focus on bus request/reply bugs",
	})
	if prompt == "" {
		t.Fatal("expected non-empty prompt")
	}
	if want := "Current specialist profile hint: focus on bus request/reply bugs"; !strings.Contains(prompt, want) {
		t.Fatalf("expected prompt to contain %q, got %q", want, prompt)
	}
}
