package agent

import (
	"context"
	"path/filepath"
	"testing"

	sqlite "github.com/abcdlsj/mink/runtime/store/sqlite"
)

func testTeamDB(t *testing.T) *sqlite.DB {
	t.Helper()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "runtime.db"), sqlite.OpenOptions{PoolSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestTeamDispatcherAutoScheduleRoundRobin(t *testing.T) {
	ctx := context.Background()
	db := testTeamDB(t)

	teamID, err := db.CreateTeam(ctx, "rr-team", "agent:leader", "round_robin", 6)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AddTeamMember(ctx, teamID, "agent:coder", "Coder", "Writes code", "persistent"); err != nil {
		t.Fatal(err)
	}
	if err := db.AddTeamMember(ctx, teamID, "agent:reviewer", "Reviewer", "Reviews changes", "persistent"); err != nil {
		t.Fatal(err)
	}

	disp := NewTeamDispatcher(db, nil, nil)

	handoff, ok, err := disp.AutoSchedule(ctx, "cli:test", TeamTurn{
		TeamID:         teamID,
		LeaderAgentID:  "agent:leader",
		SpeakerAgentID: "agent:leader",
		Round:          1,
		MaxRounds:      6,
		TurnPolicy:     "round_robin",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected auto-schedule after leader turn")
	}
	if handoff.SpeakerAgentID != "agent:coder" {
		t.Fatalf("expected agent:coder, got %s", handoff.SpeakerAgentID)
	}

	handoff, ok, err = disp.AutoSchedule(ctx, "cli:test", TeamTurn{
		TeamID:         teamID,
		LeaderAgentID:  "agent:leader",
		SpeakerAgentID: "agent:reviewer",
		Round:          3,
		MaxRounds:      6,
		TurnPolicy:     "round_robin",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected leader closeout handoff")
	}
	if handoff.SpeakerAgentID != "agent:leader" {
		t.Fatalf("expected leader closeout, got %s", handoff.SpeakerAgentID)
	}
}

func TestTeamDispatcherAutoScheduleStopsAfterLeaderCloseout(t *testing.T) {
	ctx := context.Background()
	db := testTeamDB(t)

	teamID, err := db.CreateTeam(ctx, "rr-team", "agent:leader", "round_robin", 6)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AddTeamMember(ctx, teamID, "agent:coder", "Coder", "Writes code", "persistent"); err != nil {
		t.Fatal(err)
	}

	disp := NewTeamDispatcher(db, nil, nil)

	_, ok, err := disp.AutoSchedule(ctx, "cli:test", TeamTurn{
		TeamID:         teamID,
		LeaderAgentID:  "agent:leader",
		SpeakerAgentID: "agent:leader",
		Round:          3,
		MaxRounds:      6,
		TurnPolicy:     "round_robin",
	})
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected round robin to stop after leader closeout")
	}
}
