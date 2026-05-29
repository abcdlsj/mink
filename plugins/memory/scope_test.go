package memory

import (
	"context"
	"testing"

	"github.com/abcdlsj/sumi/command"
)

func TestResolveReadScopePrefersPersona(t *testing.T) {
	s := &store{root: "/tmp/irrelevant", workspace: "/ws"}
	ctx := command.WithPersona(command.WithSource(context.Background(), "cli"), "debug")
	sc := s.resolveReadScope(ctx, command.SourceFrom(ctx), "", "")
	if sc.Kind != "persona" || sc.Key != "debug" {
		t.Fatalf("scope = %+v, want persona:debug", sc)
	}
}

func TestResolveSearchScopesPersonaFirst(t *testing.T) {
	s := &store{root: "/tmp/irrelevant", workspace: "/ws"}
	ctx := command.WithPersona(command.WithSource(context.Background(), "tg:dm:42"), "reviewer")
	scopes := s.resolveSearchScopes(ctx, command.SourceFrom(ctx), "", "")
	if len(scopes) == 0 || scopes[0].Kind != "persona" || scopes[0].Key != "reviewer" {
		t.Fatalf("first scope = %+v, want persona:reviewer", scopes[0])
	}
}

func TestResolveReadScopeExplicitKindWins(t *testing.T) {
	s := &store{root: "/tmp/irrelevant", workspace: "/ws"}
	ctx := command.WithPersona(context.Background(), "debug")
	sc := s.resolveReadScope(ctx, "cli", "channel", "chan-a")
	if sc.Kind != "channel" || sc.Key != "chan-a" {
		t.Fatalf("scope = %+v, want channel:chan-a", sc)
	}
}
