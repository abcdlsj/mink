package command

import (
	"context"
	"testing"
)

func TestRunContextDrivesSourceSessionNoticeAndMemory(t *testing.T) {
	ctx := WithSource(context.Background(), "tg:dm:42")
	ctx = WithRunContext(ctx, RunContext{
		Source:   "cron:bazaar",
		Session:  "cron:bazaar",
		Delivery: "tg:dm:42",
		Memory: []MemoryScope{
			{Kind: "channel", Key: "cron:bazaar"},
			{Kind: "channel", Key: "tg:dm:42"},
		},
		Permission: "cron",
		Input:      "ping",
	})
	if got := SourceFrom(ctx); got != "cron:bazaar" {
		t.Fatalf("source = %q", got)
	}
	if got := SessionSourceFrom(ctx); got != "cron:bazaar" {
		t.Fatalf("session = %q", got)
	}
	if got := NoticeSourceFrom(ctx); got != "tg:dm:42" {
		t.Fatalf("notice = %q", got)
	}
	if got := MemoryScopesFrom(ctx); len(got) != 2 || got[1].Key != "tg:dm:42" {
		t.Fatalf("memory scopes = %+v", got)
	}
	if got := PermissionFrom(ctx); got != "cron" {
		t.Fatalf("permission = %q", got)
	}
	if got := InputFrom(ctx); got != "ping" {
		t.Fatalf("input = %q", got)
	}
}

func TestRunContextFallbacks(t *testing.T) {
	ctx := WithSource(context.Background(), "cli")
	if got := SessionSourceFrom(ctx); got != "cli" {
		t.Fatalf("session fallback = %q", got)
	}
	if got := NoticeSourceFrom(ctx); got != "cli" {
		t.Fatalf("notice fallback = %q", got)
	}
	if got := MemoryScopesFrom(ctx); got != nil {
		t.Fatalf("memory fallback = %+v", got)
	}
	if got := PermissionFrom(ctx); got != "default" {
		t.Fatalf("permission fallback = %q", got)
	}
	if got := InputFrom(ctx); got != "" {
		t.Fatalf("input fallback = %q", got)
	}
}
